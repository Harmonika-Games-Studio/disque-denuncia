package limpeza

import (
	"archive/zip"
	"bytes"
	"errors"
	"fmt"
	"strings"
	"testing"
)

// fotoFalsa faz o papel do imagem.Limpar: devolve um "JPEG" marcado, ou erro para a
// foto que não abre (aqui, a curta demais).
func fotoFalsa(c []byte) ([]byte, error) {
	if !ehFoto(c) || len(c) < 16 {
		return nil, errors.New("não é foto, ou não abre")
	}
	return []byte("JPEG refeito"), nil
}

func nomes(t *testing.T, partes map[string]*zip.File) []string {
	t.Helper()
	var out []string
	for n := range partes {
		out = append(out, n)
	}
	return out
}

func TestZIPRenomeiaLimpaEDescartaLixo(t *testing.T) {
	_, foto := jpegComMetadados(t)
	nested := montarZip(t, parte{"Contrato do Fulano.docx", docx(t), zip.Deflate})
	original := montarZip(t,
		parte{"Fotos do Fulano/", nil, zip.Store},
		parte{"Fotos do Fulano/IMG_2026-10-28 14.33.jpg", foto, zip.Store},
		parte{"Fotos do Fulano/Screenshot 2026-10-28 at 14.33.11.PNG", pngComTexto(t), zip.Deflate},
		parte{"__MACOSX/Fotos do Fulano/._IMG_2026-10-28 14.33.jpg", []byte("de onde baixou: https://site/fulano"), zip.Deflate},
		parte{"Fotos do Fulano/.DS_Store", []byte("Finder"), zip.Deflate},
		parte{`C:\Users\fulano\Thumbs.db`, []byte("Windows"), zip.Deflate},
		parte{"Vídeo do Fulano.MOV", videoDoCelular(), zip.Deflate},
		parte{"anotações.txt", []byte("texto do Fulano"), zip.Deflate},
		parte{"documentos.zip", nested, zip.Store},
	)

	limpo, completo, err := ZIP(original, fotoFalsa)
	if err != nil {
		t.Fatal(err)
	}
	if completo {
		t.Error("tem um .txt dentro: não podia sair completo")
	}
	partes := abrirZip(t, limpo) // confere comentário vazio e datas zeradas
	esperados := []string{"001.jpg", "002.jpg", "003.mov", "004.txt", "005.zip"}
	if len(partes) != len(esperados) {
		t.Fatalf("nomes: %v", nomes(t, partes))
	}
	for _, n := range esperados {
		if partes[n] == nil {
			t.Fatalf("faltou %s em %v", n, nomes(t, partes))
		}
		if len(partes[n].Extra) != 0 || partes[n].Comment != "" || partes[n].ExternalAttrs != 0 {
			t.Errorf("%s com extra %x, comentário %q, atributos %x", n, partes[n].Extra, partes[n].Comment, partes[n].ExternalAttrs)
		}
	}
	if lerParte(t, partes["001.jpg"]) != "JPEG refeito" || lerParte(t, partes["002.jpg"]) != "JPEG refeito" {
		t.Error("as fotos não foram refeitas")
	}
	if v := lerParte(t, partes["003.mov"]); strings.Contains(v, "23.5505") || strings.Contains(v, "Fulano") || len(v) != len(videoDoCelular()) {
		t.Error("o vídeo guardou o GPS ou mudou de tamanho")
	}
	if lerParte(t, partes["004.txt"]) != "texto do Fulano" {
		t.Error("o .txt tinha que ir como veio")
	}
	dentro := abrirZip(t, []byte(lerParte(t, partes["005.zip"])))
	if dentro["001.docx"] == nil || len(dentro) != 1 {
		t.Fatalf("ZIP de dentro: %v", nomes(t, dentro))
	}
	doc := lerParte(t, dentro["001.docx"])
	docPartes := abrirZip(t, []byte(doc))
	if strings.Contains(lerParte(t, docPartes["word/document.xml"]), "Fulano") {
		t.Error("o docx de dentro do ZIP de dentro não foi limpo")
	}
	// O .txt vai como veio (e deflate guarda texto curto literalmente), então o nome
	// da pessoa pode aparecer no conteúdo; o que não pode é vir de nome, pasta ou lixo.
	for _, sobra := range []string{"Fotos do Fulano", "__MACOSX", "de onde baixou", "Finder", "Windows", "2026-10-28", "Contrato do Fulano", "anotações"} {
		if bytes.Contains(limpo, []byte(sobra)) {
			t.Errorf("sobrou %q no ZIP", sobra)
		}
	}
}

func TestZIPCompletoQuandoTudoELimpo(t *testing.T) {
	_, foto := jpegComMetadados(t)
	b := montarZip(t, parte{"a.jpg", foto, zip.Deflate}, parte{"b.gif", gifComComentario(t, 2, 4), zip.Deflate})
	_, completo, err := ZIP(b, fotoFalsa)
	if err != nil || !completo {
		t.Errorf("completo=%v err=%v", completo, err)
	}
}

func TestZIPFotoQueNaoAbreVaiComoVeio(t *testing.T) {
	quebrada := []byte{0xFF, 0xD8, 1, 2, 3}
	b := montarZip(t, parte{"a.jpg", quebrada, zip.Deflate})
	limpo, completo, err := ZIP(b, fotoFalsa)
	if err != nil || completo {
		t.Fatalf("completo=%v err=%v", completo, err)
	}
	partes := abrirZip(t, limpo)
	if partes["001.jpg"] == nil || lerParte(t, partes["001.jpg"]) != string(quebrada) {
		t.Errorf("%v", nomes(t, partes))
	}
	// Sem a função de foto (ou foto grande demais), a foto fica e o ZIP sai incompleto.
	if _, completo, err := ZIP(montarZip(t, parte{"a.jpg", quebrada, zip.Deflate}), nil); err != nil || completo {
		t.Errorf("sem foto: completo=%v err=%v", completo, err)
	}
}

func TestZIPProfundidade(t *testing.T) {
	txt := montarZip(t, parte{"Fulano.txt", []byte("oi"), zip.Deflate})
	b := txt
	for range maxNiveisDeZip {
		b = montarZip(t, parte{"Fulano.zip", b, zip.Store})
	}
	limpo, _, err := ZIP(b, nil)
	if err != nil {
		t.Fatal(err)
	}
	// Os níveis até o teto foram renomeados; o de dentro do último fica como veio.
	for range maxNiveisDeZip {
		partes := abrirZip(t, limpo)
		if partes["001.zip"] == nil {
			t.Fatalf("%v", nomes(t, partes))
		}
		limpo = []byte(lerParte(t, partes["001.zip"]))
	}
	if !bytes.Equal(limpo, txt) {
		t.Error("o ZIP além do teto de níveis tinha que ir como veio")
	}
}

func TestZIPRecusa(t *testing.T) {
	var muitos []parte
	for i := range maxEntradasNoZip + 1 {
		muitos = append(muitos, parte{fmt.Sprint(i), nil, zip.Store})
	}
	corrompido := montarZip(t, parte{"a.txt", []byte("oi"), zip.Deflate})
	corrompido = corrompido[:len(corrompido)-5]
	for nome, b := range map[string][]byte{
		"não é zip": []byte("PK\x03\x04 nada"), "corrompido": corrompido, "arquivos demais": montarZip(t, muitos...),
	} {
		if _, _, err := ZIP(b, nil); !errors.Is(err, ErrFormato) {
			t.Errorf("%s: %v", nome, err)
		}
	}
}

func TestLixoDoSistemaEExtensao(t *testing.T) {
	for nome, lixo := range map[string]bool{
		"__MACOSX/a/._b.jpg": true, "a/.DS_Store": true, `a\Thumbs.db`: true, "desktop.ini": true, "._x": true,
		"a/b.jpg": false, "MACOSX/x": false, "ds_store.txt": false,
	} {
		if lixoDoSistema(nome) != lixo {
			t.Errorf("%s: lixo=%v", nome, !lixo)
		}
	}
	for nome, ext := range map[string]string{
		"a/Fulano.JPG": "jpg", "sem": "", "x.tar.gz": "gz", "a.": "", "a.123456789012": "", "a.p df": "",
	} {
		if got := extensaoDoNome(nome); got != ext {
			t.Errorf("%s: %q", nome, got)
		}
	}
}
