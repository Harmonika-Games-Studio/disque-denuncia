package limpeza

import (
	"archive/zip"
	"bytes"
	"fmt"
	"regexp"
	"strings"
)

// ZIP comum (o que não é documento do Office nem do LibreOffice): uma pasta de prints,
// de fotos, de PDFs. O que identifica quem fez:
//
//   - o nome de cada arquivo de dentro: "Screenshot 2026-10-28 14.33.png", "WhatsApp
//     Image 2026-10-28 at 14.33.11.jpeg", "relatório do Fulano.pdf", o caminho
//     "Users/fulano/Desktop/…";
//   - a data e a hora de cada arquivo (DOS e, nos campos extras, com segundos e fuso),
//     o uid/gid do Unix, os atributos e o sistema de quem compactou;
//   - o comentário do ZIP e o de cada arquivo;
//   - o lixo do sistema: a pasta __MACOSX e os "._arquivo" (o macOS grava ali de que
//     site o arquivo foi baixado e por qual programa), .DS_Store, Thumbs.db, desktop.ini;
//   - os metadados dos próprios arquivos de dentro: o EXIF da foto, o autor do PDF, o
//     GPS do vídeo.
//
// O ZIP é regravado do zero: cada arquivo vira "001.ext", "002.ext"… na ordem do
// original, só com a extensão, sem data, sem campos extras, sem comentário; o lixo do
// sistema some; e cada arquivo de dentro passa pela limpeza do tipo dele (foto refeita
// pelo pacote imagem, PDF, Office, vídeo, áudio, GIF, e ZIP dentro de ZIP até
// maxNiveisDeZip). A pasta de origem some junto com os nomes: era o jeito mais fácil de
// o nome da pessoa ir junto.
//
// Um arquivo de dentro que a limpeza não conhece (um .txt, um HEIC) vai como veio,
// e o ZIP sai sem a marca de completo: o painel avisa que ele pode levar metadados.

const (
	maxEntradasNoZip = 2000
	maxNiveisDeZip   = 3
	// maxFotoNoZip: foto maior que isso não é refeita (memória do servidor), igual ao
	// teto da foto enviada solta (web.maxImagemLimpa).
	maxFotoNoZip = 64 << 20
)

// Foto refaz uma foto (JPG, PNG, WebP) sem metadados e devolve um JPEG. É o
// imagem.Limpar; vem por parâmetro para este pacote não depender do de imagem.
type Foto func([]byte) ([]byte, error)

var reExtensao = regexp.MustCompile(`\.([a-z0-9]{1,10})$`)

// ZIP regrava um ZIP comum sem metadados. completo diz se todo arquivo de dentro foi
// limpo. ErrFormato se não dá para abrir (corrompido, com senha, grande demais ou com
// arquivos demais): quem chama deixa como veio.
func ZIP(dados []byte, foto Foto) (limpo []byte, completo bool, err error) {
	return limparZip(dados, foto, maxNiveisDeZip)
}

func limparZip(dados []byte, foto Foto, niveis int) ([]byte, bool, error) {
	zr, err := zip.NewReader(bytes.NewReader(dados), int64(len(dados)))
	if err != nil {
		return nil, false, fmt.Errorf("%w: %w", ErrFormato, err)
	}
	if len(zr.File) > maxEntradasNoZip {
		return nil, false, fmt.Errorf("%w: ZIP com mais de %d arquivos", ErrFormato, maxEntradasNoZip)
	}
	var out bytes.Buffer
	zw := zip.NewWriter(&out)
	completo := true
	aberto := int64(0)
	n := 0
	for _, f := range zr.File {
		if f.FileInfo().IsDir() || lixoDoSistema(f.Name) {
			continue
		}
		c, err := lerDoZip(f, maxDescompactado-aberto)
		if err != nil {
			return nil, false, err
		}
		aberto += int64(len(c))
		c, ext, ok := limparEntrada(c, extensaoDoNome(f.Name), foto, niveis)
		completo = completo && ok
		n++
		nome := fmt.Sprintf("%03d", n)
		if ext != "" {
			nome += "." + ext
		}
		// Cabeçalho novo: só nome e método. Sem data (fica 1980), sem campos extras,
		// sem comentário, sem atributos nem sistema de origem.
		w, err := zw.CreateHeader(&zip.FileHeader{Name: nome, Method: zip.Deflate})
		if err != nil {
			return nil, false, err
		}
		if _, err := w.Write(c); err != nil {
			return nil, false, err
		}
	}
	if err := zw.Close(); err != nil {
		return nil, false, err
	}
	return out.Bytes(), completo, nil
}

// lixoDoSistema: o que o sistema operacional põe no ZIP sem a pessoa ver.
func lixoDoSistema(nome string) bool {
	nome = strings.ReplaceAll(nome, `\`, "/")
	partes := strings.Split(nome, "/")
	base := partes[len(partes)-1]
	for _, p := range partes[:len(partes)-1] {
		if p == "__MACOSX" {
			return true
		}
	}
	switch base {
	case ".DS_Store", "Thumbs.db", "desktop.ini":
		return true
	}
	return strings.HasPrefix(base, "._")
}

// extensaoDoNome é a única coisa que fica do nome: letras e números depois do último
// ponto, em minúsculas, até 10. O resto do nome é descartado.
func extensaoDoNome(nome string) string {
	m := reExtensao.FindStringSubmatch(strings.ToLower(nome))
	if m == nil {
		return ""
	}
	return m[1]
}

// limparEntrada limpa um arquivo de dentro do ZIP conforme o que os bytes dizem. ok é
// falso se o tipo não é conhecido ou a limpeza dele falhou: o arquivo vai como veio.
// Um pânico de uma biblioteca (PDF malformado de propósito) conta como falha dessa
// entrada só, sem perder o resto do ZIP.
func limparEntrada(c []byte, ext string, foto Foto, niveis int) (limpo []byte, extLimpa string, ok bool) {
	defer func() {
		if recover() != nil {
			limpo, extLimpa, ok = c, ext, false
		}
	}()
	if ehFoto(c) {
		if foto == nil || len(c) > maxFotoNoZip {
			return c, ext, false
		}
		if limpa, err := foto(c); err == nil {
			return limpa, "jpg", true
		}
		return c, ext, false
	}
	switch Identificar(c) {
	case FormatoGIF:
		if limpa, err := GIF(c); err == nil {
			return limpa, "gif", true
		}
	case FormatoPDF:
		if limpa, err := PDF(c); err == nil {
			return limpa, "pdf", true
		}
	case FormatoZip:
		if limpa, err := Office(c); err == nil {
			return limpa, ext, true
		}
		if niveis > 1 {
			if limpa, completo, err := limparZip(c, foto, niveis-1); err == nil {
				return limpa, "zip", completo
			}
		}
	case FormatoMP4:
		if rs, err := RemendosMP4(bytes.NewReader(c), int64(len(c))); err == nil {
			return aplicarRemendos(c, rs), ouEntao(ext, "mp4"), true
		}
	case FormatoMP3:
		if rs, err := RemendosMP3(bytes.NewReader(c), int64(len(c))); err == nil {
			return aplicarRemendos(c, rs), "mp3", true
		}
	}
	return c, ext, false
}

func ehFoto(c []byte) bool {
	return bytes.HasPrefix(c, []byte{0xFF, 0xD8}) || bytes.HasPrefix(c, []byte("\x89PNG")) ||
		(len(c) >= 12 && bytes.HasPrefix(c, []byte("RIFF")) && bytes.Equal(c[8:12], []byte("WEBP")))
}

func aplicarRemendos(b []byte, rs []Remendo) []byte {
	out := bytes.Clone(b)
	for _, r := range rs {
		copy(out[r.Pos:], r.Dados)
	}
	return out
}

func ouEntao(s, padrao string) string {
	if s == "" {
		return padrao
	}
	return s
}
