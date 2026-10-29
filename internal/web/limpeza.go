package web

import (
	"bytes"
	"errors"
	"fmt"

	"disque-denuncia/internal/imagem"
	"disque-denuncia/internal/limpeza"
	"disque-denuncia/internal/store"
)

// Ao concluir, antes de a denúncia existir, cada anexo do rascunho perde os metadados:
//
//   - foto (JPG, PNG, WebP): refeita do zero (pacote imagem). Foto que não abre é
//     recusada (400), porque a pessoa acha que mandou uma foto limpa;
//   - vídeo e áudio do celular, MP3: remendos no lugar (pacote limpeza), em qualquer tamanho;
//   - GIF, Office, LibreOffice e PDF: regravados, até o teto de memória abaixo.
//
// O que não é reconhecido, passa do teto ou não abre (PDF com senha, documento
// corrompido) vai como veio, sem a marca de limpo; a página avisa que isso pode
// acontecer, e o painel e o OS mostram o aviso nesse anexo.

const (
	// maxImagemLimpa: imagem maior que isso não é decodificada (memória do servidor).
	maxImagemLimpa = 64 << 20
	// maxDocumentoLimpo: GIF, documento ou PDF maior que isso vai como veio. A limpeza
	// lê o arquivo inteiro na memória.
	maxDocumentoLimpo = 128 << 20
)

var errArquivo = errors.New("arquivo")

// imagensLimpas são os tipos que o pacote imagem consegue refazer sem metadados.
var imagensLimpas = map[string]bool{"image/jpeg": true, "image/png": true, "image/webp": true}

func (s *Server) limparEnvio(envio string) error {
	arquivos, err := s.store.ArquivosDoEnvio(envio)
	if err != nil {
		return err
	}
	for _, a := range arquivos {
		if err := s.limparArquivo(a); err != nil {
			return err
		}
	}
	return nil
}

func (s *Server) limparArquivo(a store.Arquivo) error {
	if imagensLimpas[a.Tipo] {
		return s.refazerFoto(a)
	}
	leitor := s.store.Leitor(a)
	cabeca := make([]byte, 16)
	n, _ := leitor.ReadAt(cabeca, 0)
	var remendos []limpeza.Remendo
	var err error
	switch limpeza.Identificar(cabeca[:n]) {
	case limpeza.FormatoMP4:
		remendos, err = limpeza.RemendosMP4(leitor, a.Tamanho)
	case limpeza.FormatoMP3:
		remendos, err = limpeza.RemendosMP3(leitor, a.Tamanho)
	case limpeza.FormatoGIF:
		return s.regravar(a, limpeza.GIF)
	case limpeza.FormatoZip:
		return s.regravar(a, limpeza.Office)
	case limpeza.FormatoPDF:
		return s.regravar(a, limpeza.PDF)
	default:
		return nil
	}
	if errors.Is(err, limpeza.ErrFormato) {
		return nil
	}
	if err != nil {
		return err
	}
	return s.store.Remendar(a.ID, paraOStore(remendos))
}

func paraOStore(rs []limpeza.Remendo) []store.Remendo {
	out := make([]store.Remendo, len(rs))
	for i, r := range rs {
		out[i] = store.Remendo(r)
	}
	return out
}

func (s *Server) refazerFoto(a store.Arquivo) error {
	if a.Tamanho > maxImagemLimpa {
		return fmt.Errorf("%w %d: imagem grande demais para apagar os metadados (até 64 MB)", errArquivo, a.Posicao+1)
	}
	var bruto bytes.Buffer
	if err := s.store.LerArquivo(a.ID, &bruto); err != nil {
		return err
	}
	limpa, err := imagem.Limpar(bruto.Bytes())
	if err != nil {
		return fmt.Errorf("%w %d: %w", errArquivo, a.Posicao+1, err)
	}
	a.Tipo, a.Extensao, a.Limpo = "image/jpeg", "jpg", true
	return s.store.SubstituirArquivo(a, limpa)
}

// regravar lê o arquivo inteiro, limpa e grava no lugar. Um pânico da biblioteca (PDF
// malformado de propósito, por exemplo) deixa o arquivo como veio: a denúncia não
// pode se perder por causa de um anexo.
func (s *Server) regravar(a store.Arquivo, limpar func([]byte) ([]byte, error)) error {
	if a.Tamanho > maxDocumentoLimpo {
		return nil
	}
	var bruto bytes.Buffer
	if err := s.store.LerArquivo(a.ID, &bruto); err != nil {
		return err
	}
	limpo, ok := semPanico(limpar, bruto.Bytes())
	if !ok {
		return nil
	}
	a.Limpo = true
	return s.store.SubstituirArquivo(a, limpo)
}

func semPanico(limpar func([]byte) ([]byte, error), dados []byte) (limpo []byte, ok bool) {
	defer func() {
		if recover() != nil {
			limpo, ok = nil, false
		}
	}()
	limpo, err := limpar(dados)
	return limpo, err == nil && len(limpo) > 0
}
