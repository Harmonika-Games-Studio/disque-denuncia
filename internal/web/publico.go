package web

import (
	"errors"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"

	"disque-denuncia/internal/imagem"
	"disque-denuncia/internal/store"
)

// A metade pública. Deste arquivo, o que sai de cada requisição são só os campos
// do formulário: categoria, descricao e imagens.

const (
	maxImagem = 8 << 20
	// O corpo inteiro cabe na memória (maxCorpo = maxMemoria): o net/http grava em
	// arquivo temporário o que passa do limite de memória, e arquivo tem hora de criação.
	maxCorpo   = store.MaxImagens*maxImagem + 1<<20
	maxMemoria = maxCorpo
)

var errImagem = errors.New("imagem")

func (s *Server) formulario(w http.ResponseWriter, r *http.Request) {
	render(w, r, FormularioPage(s.empresa))
}

func (s *Server) enviar(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, maxCorpo)
	if err := r.ParseMultipartForm(maxMemoria); err != nil {
		fail(w, r, corpoInvalido(err))
		return
	}
	defer r.MultipartForm.RemoveAll()

	n := store.Nova{Categoria: r.FormValue("categoria"), Descricao: r.FormValue("descricao")}
	arquivos := comConteudo(r.MultipartForm.File["imagens"])
	if len(arquivos) > store.MaxImagens {
		fail(w, r, fmt.Errorf("%w: envie no máximo %d imagens", store.ErrInvalid, store.MaxImagens))
		return
	}
	// Valida o texto antes de gastar CPU com as imagens.
	if err := n.Validar(); err != nil {
		fail(w, r, err)
		return
	}
	imagens, err := limparTodas(arquivos)
	if err != nil {
		fail(w, r, err)
		return
	}
	n.Imagens = imagens
	if err := s.store.Registrar(n, s.agora()); err != nil {
		fail(w, r, err)
		return
	}
	if r.Header.Get("HX-Request") == "true" {
		render(w, r, Recebida())
		return
	}
	render(w, r, RecebidaPage(s.empresa))
}

// comConteudo descarta a parte vazia que o navegador manda quando nenhuma imagem foi escolhida.
func comConteudo(fs []*multipart.FileHeader) []*multipart.FileHeader {
	var out []*multipart.FileHeader
	for _, f := range fs {
		if f.Size > 0 {
			out = append(out, f)
		}
	}
	return out
}

// limparTodas re-codifica cada imagem (pacote imagem). O nome do arquivo e o
// Content-Type que o navegador mandou são ignorados: o nome pode trazer o nome da
// pessoa ("IMG_Fulano.jpg"), e o formato é lido dos próprios bytes.
func limparTodas(fs []*multipart.FileHeader) ([][]byte, error) {
	out := make([][]byte, 0, len(fs))
	for i, f := range fs {
		if f.Size > maxImagem {
			return nil, fmt.Errorf("%w %d passa de 8 MB", errImagem, i+1)
		}
		bruto, err := ler(f)
		if err != nil {
			return nil, err
		}
		limpa, err := imagem.Limpar(bruto)
		if err != nil {
			return nil, fmt.Errorf("%w %d: %w", errImagem, i+1, err)
		}
		out = append(out, limpa)
	}
	return out, nil
}

func ler(f *multipart.FileHeader) ([]byte, error) {
	arq, err := f.Open()
	if err != nil {
		return nil, err
	}
	defer arq.Close()
	return io.ReadAll(arq)
}

func corpoInvalido(err error) error {
	var grande *http.MaxBytesError
	if errors.As(err, &grande) {
		return err
	}
	return fmt.Errorf("%w: formulário ilegível", store.ErrInvalid)
}
