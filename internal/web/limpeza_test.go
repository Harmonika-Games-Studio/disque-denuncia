package web

import (
	"archive/zip"
	"bytes"
	"encoding/binary"
	"fmt"
	"image"
	"image/color/palette"
	"image/gif"
	"io"
	"strings"
	"testing"
	"time"

	"disque-denuncia/internal/store"
)

func caixaMP4(tipo string, corpo ...[]byte) []byte {
	c := bytes.Join(corpo, nil)
	return append(append(binary.BigEndian.AppendUint32(nil, uint32(8+len(c))), tipo...), c...)
}

// videoComGPS é um MOV mínimo com GPS e aparelho em moov/udta.
func videoComGPS() []byte {
	return bytes.Join([][]byte{
		caixaMP4("ftyp", []byte("qt  ")),
		caixaMP4("moov", caixaMP4("udta", caixaMP4("\xa9xyz", []byte("+23.5505-046.6333/")), caixaMP4("\xa9mod", []byte("iPhone do Fulano")))),
		caixaMP4("mdat", bytes.Repeat([]byte("video "), 100)),
	}, nil)
}

func docxDoFulano(t *testing.T) []byte {
	t.Helper()
	var b bytes.Buffer
	zw := zip.NewWriter(&b)
	for nome, c := range map[string]string{
		"[Content_Types].xml": "<Types/>",
		"docProps/core.xml":   "<cp:coreProperties><dc:creator>Fulano de Tal</dc:creator></cp:coreProperties>",
		"word/document.xml":   `<w:document><w:ins w:author="Fulano"><w:t>Texto da denúncia</w:t></w:ins></w:document>`,
	} {
		w, _ := zw.Create(nome)
		io.WriteString(w, c)
	}
	zw.Close()
	return b.Bytes()
}

func lerTudoDoZip(t *testing.T, b []byte) string {
	t.Helper()
	zr, err := zip.NewReader(bytes.NewReader(b), int64(len(b)))
	if err != nil {
		t.Fatal(err)
	}
	var s strings.Builder
	for _, f := range zr.File {
		rc, _ := f.Open()
		c, _ := io.ReadAll(rc)
		rc.Close()
		s.Write(c)
	}
	return s.String()
}

func conteudo(t *testing.T, st *store.Store, a store.Arquivo) []byte {
	t.Helper()
	var b bytes.Buffer
	if err := st.LerArquivo(a.ID, &b); err != nil {
		t.Fatal(err)
	}
	return b.Bytes()
}

func TestConcluirLimpaVideoEDocumento(t *testing.T) {
	a := novo(t)
	desconhecido := []byte("\x00\x01binário que ninguém conhece, do Fulano")
	var b bytes.Buffer
	zw := zip.NewWriter(&b)
	arq, _ := zw.Create("fotos/Fulano.txt")
	io.WriteString(arq, "oi")
	zw.Close()
	zipComum := b.Bytes()
	w := a.enviarComo(t, validos(), videoComGPS(), docxDoFulano(t), desconhecido, zipComum)
	if w.Code != 200 {
		t.Fatalf("%d %s", w.Code, w.Body)
	}
	list, _ := a.st.Listar("")
	d, _ := a.st.Buscar(list[0].ID)
	video, doc, outro, zipC := d.Anexos[0], d.Anexos[1], d.Anexos[2], d.Anexos[3]

	if !video.Limpo || video.Tamanho != int64(len(videoComGPS())) {
		t.Errorf("vídeo: %+v", video)
	}
	if v := conteudo(t, a.st, video); bytes.Contains(v, []byte("23.5505")) || bytes.Contains(v, []byte("Fulano")) {
		t.Error("o vídeo guardou o GPS")
	}
	if !doc.Limpo {
		t.Errorf("documento: %+v", doc)
	}
	if texto := lerTudoDoZip(t, conteudo(t, a.st, doc)); strings.Contains(texto, "Fulano") || !strings.Contains(texto, "Texto da denúncia") {
		t.Errorf("documento: %s", texto)
	}
	if outro.Limpo || !bytes.Equal(conteudo(t, a.st, outro), desconhecido) {
		t.Error("o que a limpeza não conhece vai como veio, sem a marca")
	}
	// O ZIP comum é reaberto: o nome "fotos/Fulano.txt" some, mas o .txt de dentro
	// é um tipo que a limpeza não conhece, então o ZIP sai sem a marca.
	zr, err := zip.NewReader(bytes.NewReader(conteudo(t, a.st, zipC)), zipC.Tamanho)
	if err != nil || len(zr.File) != 1 || zr.File[0].Name != "001.txt" || zipC.Limpo {
		t.Errorf("zip comum: %v %+v %+v", err, zipC, zr)
	}

	pagina := a.do(t, pedidoAdmin("GET", "/admin/denuncias/"+d.ID, nil)).Body.String()
	if strings.Count(pagina, "metadados removidos pelo canal") != 2 || strings.Count(pagina, "veio como foi enviado") != 2 {
		t.Errorf("painel: %s", pagina)
	}
}

func gifPequeno(t *testing.T) []byte {
	t.Helper()
	var b bytes.Buffer
	if err := gif.Encode(&b, image.NewPaletted(image.Rect(0, 0, 4, 4), palette.Plan9), nil); err != nil {
		t.Fatal(err)
	}
	return b.Bytes()
}

func TestConcluirOutrosFormatos(t *testing.T) {
	a := novo(t)
	mp3 := append([]byte("ID3\x03\x00\x00\x00\x00\x00\x0aTPE1Fulano"), bytes.Repeat([]byte{0xFF, 0xFB, 0x90, 0}, 10)...)
	mp4Quebrado := append(caixaMP4("ftyp", []byte("isom")), []byte("Fulano sem caixa")...)
	pdfQuebrado := []byte("%PDF-1.4 sem objeto nenhum do Fulano")
	if w := a.enviarComo(t, validos(), mp3, gifPequeno(t), mp4Quebrado, pdfQuebrado); w.Code != 200 {
		t.Fatalf("%d %s", w.Code, w.Body)
	}
	list, _ := a.st.Listar("")
	d, _ := a.st.Buscar(list[0].ID)
	limpos := []bool{}
	for _, an := range d.Anexos {
		limpos = append(limpos, an.Limpo)
	}
	if fmt.Sprint(limpos) != "[true true false false]" {
		t.Errorf("limpos: %v", limpos)
	}
	if bytes.Contains(conteudo(t, a.st, d.Anexos[0]), []byte("Fulano")) {
		t.Error("o MP3 guardou o artista")
	}
	if !bytes.Equal(conteudo(t, a.st, d.Anexos[3]), pdfQuebrado) {
		t.Error("o PDF ilegível tinha que ir como veio")
	}
}

func TestSemJavascriptTambemLimpa(t *testing.T) {
	a := novo(t)
	if w := a.do(t, multipartSemJS(t, validos(), "doc.docx", docxDoFulano(t))); w.Code != 200 {
		t.Fatalf("%d %s", w.Code, w.Body)
	}
	list, _ := a.st.Listar("")
	d, _ := a.st.Buscar(list[0].ID)
	if !d.Anexos[0].Limpo || strings.Contains(lerTudoDoZip(t, conteudo(t, a.st, d.Anexos[0])), "Fulano") {
		t.Errorf("%+v", d.Anexos[0])
	}
}

func TestSemJavascriptFotoQuebradaDescartaORascunho(t *testing.T) {
	a := novo(t)
	if w := a.do(t, multipartSemJS(t, validos(), "a.jpg", fotoComComentario(t)[:200])); w.Code != 400 {
		t.Fatalf("%d", w.Code)
	}
	var rascunhos int
	a.conn.QueryRow(`SELECT COUNT(*) FROM envios`).Scan(&rascunhos)
	if rascunhos != 0 {
		t.Error("ficou o rascunho com a foto quebrada")
	}
}

func TestSemJavascriptLimiteDoDia(t *testing.T) {
	a := novo(t)
	for range store.LimiteEnviosPorDia {
		a.st.NovoEnvio(time.Now())
	}
	if w := a.do(t, multipartSemJS(t, validos(), "", nil)); w.Code != 429 {
		t.Errorf("%d", w.Code)
	}
}

func TestRegravarPulaOGrandeEOPanico(t *testing.T) {
	a := novo(t)
	srv := &Server{store: a.st}
	nunca := func([]byte) ([]byte, error) { t.Fatal("leu o arquivo grande demais"); return nil, nil }
	if err := srv.regravar(store.Arquivo{ID: "x", Tamanho: maxDocumentoLimpo + 1}, nunca); err != nil {
		t.Error(err)
	}
	envio, _ := a.st.Rascunho([]store.ArquivoPronto{{Dados: []byte("%PDF-1.4 armadilha")}}, time.Now())
	arquivos, _ := a.st.ArquivosDoEnvio(envio)
	if err := srv.regravar(arquivos[0], func([]byte) ([]byte, error) { panic("pdf malformado") }); err != nil {
		t.Error(err)
	}
	if depois, _ := a.st.Arquivo(arquivos[0].ID); depois.Limpo {
		t.Error("marcou limpo depois do pânico")
	}
}

func TestSemPanico(t *testing.T) {
	if _, ok := semPanico(func([]byte) ([]byte, error) { panic("pdf malformado") }, nil); ok {
		t.Error("pânico virou ok")
	}
	if _, ok := semPanico(func([]byte) ([]byte, error) { return nil, nil }, nil); ok {
		t.Error("resultado vazio virou ok")
	}
	if l, ok := semPanico(func(b []byte) ([]byte, error) { return b, nil }, []byte("x")); !ok || string(l) != "x" {
		t.Error("caso feliz")
	}
}

// Um ZIP só com o que a limpeza conhece (foto com comentário e docx do Fulano) sai
// limpo de ponta a ponta: a foto refeita pelo pacote imagem, o docx sem autor, os
// nomes trocados, e a marca de limpo no anexo.
func TestConcluirLimpaZipDeFotosEDocumentos(t *testing.T) {
	a := novo(t)
	var b bytes.Buffer
	zw := zip.NewWriter(&b)
	for nome, c := range map[string][]byte{"Fotos do Fulano/IMG_2026.jpg": fotoComComentario(t), "Contrato do Fulano.docx": docxDoFulano(t)} {
		w, _ := zw.Create(nome)
		w.Write(c)
	}
	zw.Close()
	if w := a.enviarComo(t, validos(), b.Bytes()); w.Code != 200 {
		t.Fatalf("%d %s", w.Code, w.Body)
	}
	list, _ := a.st.Listar("")
	d, _ := a.st.Buscar(list[0].ID)
	anexo := d.Anexos[0]
	if !anexo.Limpo || anexo.Tipo != "application/zip" {
		t.Fatalf("anexo: %+v", anexo)
	}
	zr, err := zip.NewReader(bytes.NewReader(conteudo(t, a.st, anexo)), anexo.Tamanho)
	if err != nil || len(zr.File) != 2 {
		t.Fatalf("%v %v", err, zr)
	}
	for _, f := range zr.File {
		rc, _ := f.Open()
		c, _ := io.ReadAll(rc)
		rc.Close()
		switch f.Name {
		case "001.jpg":
			if !bytes.HasPrefix(c, []byte{0xFF, 0xD8}) || bytes.Contains(c, []byte("Fulano")) {
				t.Error("a foto de dentro do ZIP não foi refeita")
			}
		case "002.docx":
			if texto := lerTudoDoZip(t, c); strings.Contains(texto, "Fulano") || !strings.Contains(texto, "Texto da denúncia") {
				t.Errorf("docx de dentro do ZIP: %s", texto)
			}
		default:
			t.Errorf("nome inesperado %q", f.Name)
		}
	}
}
