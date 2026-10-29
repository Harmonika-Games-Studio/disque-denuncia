package limpeza

import (
	"archive/zip"
	"bytes"
	"errors"
	"io"
	"strings"
	"testing"
	"time"
)

type parte struct {
	nome     string
	conteudo []byte
	metodo   uint16
}

func montarZip(t *testing.T, partes ...parte) []byte {
	t.Helper()
	var b bytes.Buffer
	zw := zip.NewWriter(&b)
	zw.SetComment("salvo por Fulano")
	for _, p := range partes {
		w, err := zw.CreateHeader(&zip.FileHeader{Name: p.nome, Method: p.metodo, Modified: time.Date(2026, 10, 28, 14, 33, 0, 0, time.UTC)})
		if err != nil {
			t.Fatal(err)
		}
		w.Write(p.conteudo)
	}
	zw.Close()
	return b.Bytes()
}

func abrirZip(t *testing.T, b []byte) map[string]*zip.File {
	t.Helper()
	zr, err := zip.NewReader(bytes.NewReader(b), int64(len(b)))
	if err != nil {
		t.Fatal(err)
	}
	if zr.Comment != "" {
		t.Errorf("comentário do ZIP: %q", zr.Comment)
	}
	out := map[string]*zip.File{}
	for _, f := range zr.File {
		if f.Modified.Year() > 1980 { // data DOS zerada: 1979-11-30
			t.Errorf("%s com data %v", f.Name, f.Modified)
		}
		out[f.Name] = f
	}
	return out
}

func lerParte(t *testing.T, f *zip.File) string {
	t.Helper()
	rc, err := f.Open()
	if err != nil {
		t.Fatal(err)
	}
	defer rc.Close()
	b, _ := io.ReadAll(rc)
	return string(b)
}

func docx(t *testing.T) []byte {
	_, foto := jpegComMetadados(t)
	return montarZip(t,
		parte{"[Content_Types].xml", []byte(`<Types/>`), zip.Deflate},
		parte{"_rels/.rels", []byte(`<Relationships><Relationship Id="r1" Target="docProps/core.xml"/></Relationships>`), zip.Deflate},
		parte{"docProps/core.xml", []byte(`<cp:coreProperties><dc:creator>Fulano de Tal</dc:creator><cp:lastModifiedBy>Fulano</cp:lastModifiedBy><dcterms:created>2026-10-28T14:33:00Z</dcterms:created></cp:coreProperties>`), zip.Deflate},
		parte{"docProps/app.xml", []byte(`<Properties><Company>Empresa do Fulano</Company><TotalTime>42</TotalTime></Properties>`), zip.Deflate},
		parte{"docProps/custom.xml", []byte(`<Properties><property name="Responsável"><vt:lpwstr>Fulano</vt:lpwstr></property></Properties>`), zip.Deflate},
		parte{"word/document.xml", []byte(`<w:document><w:body><w:p><w:ins w:id="1" w:author="Fulano de Tal" w:date="2026-10-28T14:33:00Z"><w:r><w:t>Texto da denúncia</w:t></w:r></w:ins></w:p></w:body></w:document>`), zip.Deflate},
		parte{"word/comments.xml", []byte(`<w:comments><w:comment w:id="0" w:author="Fulano" w:date="2026-10-28T14:33:00Z" w:initials="FT"><w:p/></w:comment></w:comments>`), zip.Deflate},
		parte{"word/commentsExtensible.xml", []byte(`<w16cex:commentsExtensible><w16cex:commentExtensible w16cex:durableId="1" w16cex:dateUtc="2026-10-28T17:33:00Z"/></w16cex:commentsExtensible>`), zip.Deflate},
		parte{"word/people.xml", []byte(`<w15:people><w15:person w15:author="Fulano de Tal"><w15:presenceInfo w15:providerId="AD" w15:userId="S::fulano@empresa.com.br::1234"/></w15:person></w15:people>`), zip.Deflate},
		parte{"word/_rels/settings.xml.rels", []byte(`<Relationships><Relationship Id="r1" Target="file:///C:\Users\fulano\AppData\Roaming\Microsoft\Templates\Normal.dotm" TargetMode="External"/><Relationship Id="r2" Target="\\servidor\fulano\modelo.dotx" TargetMode="External"/><Relationship Id="r3" Target="https://harmonika.com.br" TargetMode="External"/></Relationships>`), zip.Deflate},
		parte{"word/media/image1.jpeg", foto, zip.Store},
		parte{"word/media/image2.png", pngComTexto(t), zip.Deflate},
		parte{"word/media/", nil, zip.Store},
	)
}

func TestOfficeDocx(t *testing.T) {
	b := docx(t)
	if Identificar(b) != FormatoZip {
		t.Fatal("não identificou")
	}
	limpo, err := Office(b)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(limpo, []byte("Fulano")) || bytes.Contains(limpo, []byte("fulano")) {
		t.Error("sobrou nome no arquivo (sem compressão)")
	}
	partes := abrirZip(t, limpo)
	if len(partes) != 13 {
		t.Fatalf("partes: %d", len(partes))
	}
	tudo := ""
	for _, f := range partes {
		tudo += lerParte(t, f)
	}
	for _, s := range []string{"Fulano", "fulano", "FT", "Empresa", "2026-10-28", "C:\\Users", "\\\\servidor"} {
		if strings.Contains(tudo, s) {
			t.Errorf("sobrou %q", s)
		}
	}
	doc := lerParte(t, partes["word/document.xml"])
	if !strings.Contains(doc, "Texto da denúncia") || !strings.Contains(doc, `w:author="Anônimo" w:date="2000-01-01T00:00:00Z"`) {
		t.Errorf("document.xml: %s", doc)
	}
	if !strings.Contains(lerParte(t, partes["word/_rels/settings.xml.rels"]), "https://harmonika.com.br") {
		t.Error("tirou o link da internet, que é conteúdo")
	}
	if partes["word/media/image1.jpeg"].Method != zip.Store {
		t.Error("mudou o método de uma parte sem compressão")
	}
}

func odt(t *testing.T) []byte {
	return montarZip(t,
		parte{"mimetype", []byte("application/vnd.oasis.opendocument.text"), zip.Store},
		parte{"meta.xml", []byte(`<office:document-meta office:version="1.3"><office:meta><meta:initial-creator>Fulano</meta:initial-creator><dc:creator>Fulano</dc:creator><meta:editing-duration>PT2H</meta:editing-duration></office:meta></office:document-meta>`), zip.Deflate},
		parte{"content.xml", []byte(`<office:document-content><office:annotation><dc:creator>Fulano</dc:creator><dc:date>2026-10-28T14:33:00</dc:date><meta:creator-initials>FT</meta:creator-initials><text:p>Comentário</text:p></office:annotation><text:p>Texto</text:p></office:document-content>`), zip.Deflate},
		parte{"settings.xml", []byte(`<config:config-item config:name="PrinterName" config:type="string">HP do Fulano</config:config-item><config:config-item config:name="PrinterSetup" config:type="base64Binary">RnVsYW5v</config:config-item>`), zip.Deflate},
	)
}

func TestOfficeLibreOffice(t *testing.T) {
	limpo, err := Office(odt(t))
	if err != nil {
		t.Fatal(err)
	}
	zr, _ := zip.NewReader(bytes.NewReader(limpo), int64(len(limpo)))
	if zr.File[0].Name != "mimetype" || zr.File[0].Method != zip.Store || zr.File[0].Flags&0x8 != 0 {
		t.Errorf("mimetype tem que vir primeiro, sem compressão e sem descritor: %+v", zr.File[0].FileHeader)
	}
	partes := abrirZip(t, limpo)
	meta := lerParte(t, partes["meta.xml"])
	if !strings.Contains(meta, `office:version="1.3"`) || strings.Contains(meta, "Fulano") || strings.Contains(meta, "PT2H") {
		t.Errorf("meta.xml: %s", meta)
	}
	for _, nome := range []string{"content.xml", "settings.xml"} {
		c := lerParte(t, partes[nome])
		if strings.Contains(c, "Fulano") || strings.Contains(c, "RnVsYW5v") || strings.Contains(c, "FT") || strings.Contains(c, "2026") {
			t.Errorf("%s: %s", nome, c)
		}
	}
}

func TestOfficeMetaSemVersao(t *testing.T) {
	b := montarZip(t,
		parte{"mimetype", []byte("application/vnd.oasis.opendocument.spreadsheet"), zip.Store},
		parte{"meta.xml", []byte(`<office:document-meta><dc:creator>Fulano</dc:creator></office:document-meta>`), zip.Deflate},
	)
	limpo, err := Office(b)
	if err != nil {
		t.Fatal(err)
	}
	if meta := lerParte(t, abrirZip(t, limpo)["meta.xml"]); !strings.Contains(meta, `office:version="1.2"`) {
		t.Errorf("meta.xml: %s", meta)
	}
}

func TestOfficeRecusa(t *testing.T) {
	zipComum := montarZip(t, parte{"fotos/a.txt", []byte("oi"), zip.Deflate})
	outroMime := montarZip(t, parte{"mimetype", []byte("application/epub+zip"), zip.Store})
	corrompido := docx(t)
	i := bytes.Index(corrompido, []byte("docProps/core.xml"))
	copy(corrompido[i+30:], bytes.Repeat([]byte{0xFF}, 20)) // estraga os dados comprimidos
	for nome, b := range map[string][]byte{
		"zip comum": zipComum, "epub": outroMime, "não é zip": []byte("PK\x03\x04 nada"), "corrompido": corrompido,
	} {
		if _, err := Office(b); !errors.Is(err, ErrFormato) {
			t.Errorf("%s: %v", nome, err)
		}
	}
}

func TestOfficeTetoDoQueEAberto(t *testing.T) {
	b := montarZip(t, parte{"word/document.xml", bytes.Repeat([]byte("a"), 1000), zip.Deflate})
	zr, _ := zip.NewReader(bytes.NewReader(b), int64(len(b)))
	if _, err := lerDoZip(zr.File[0], 999); !errors.Is(err, ErrFormato) {
		t.Errorf("passou do teto: %v", err)
	}
	if c, err := lerDoZip(zr.File[0], 1000); err != nil || len(c) != 1000 {
		t.Errorf("no teto: %v", err)
	}
}
