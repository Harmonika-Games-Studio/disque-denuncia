package limpeza

import (
	"archive/zip"
	"bytes"
	"fmt"
	"hash/crc32"
	"io"
	"regexp"
	"strings"
)

// Documentos do Office (docx, xlsx, pptx) e do LibreOffice (odt, ods, odp). Os dois são
// um ZIP de XMLs. O que identifica quem fez:
//
//   - propriedades do documento: autor, "modificado por", empresa, gerente, programa,
//     tempo de edição, datas (docProps/*.xml no Office, meta.xml no LibreOffice);
//   - autor e data de cada comentário e de cada alteração controlada, e a lista de
//     pessoas (no Office 365, com o e-mail de login);
//   - caminhos do computador ("C:\Users\fulano\..."), no modelo usado, em vínculos com
//     outros arquivos e no último local onde a planilha foi salva;
//   - o nome da impressora (settings.xml do LibreOffice);
//   - o EXIF das fotos coladas no documento;
//   - a data de cada arquivo dentro do ZIP.
//
// O documento é regravado com as propriedades vazias, "Anônimo" no lugar de cada autor,
// uma data fixa no lugar das datas, os caminhos apagados e as fotos sem EXIF. Texto,
// formatação, fórmulas e imagens continuam iguais.

const (
	autorAnonimo = "Anônimo"
	dataFixa     = "2000-01-01T00:00:00Z"
	// maxDescompactado protege contra ZIP-bomba: a soma do que é aberto na memória.
	maxDescompactado = 512 << 20
)

var (
	// Atributos com nome de pessoa: w:author, w15:author, displayName (Excel, PowerPoint).
	reAutorAtributo = regexp.MustCompile(`(\s(?:\w+:)?(?:author|displayName))="[^"]*"`)
	// Iniciais e identificadores de login (w15:userId traz o e-mail no Office 365).
	reIdAtributo = regexp.MustCompile(`(\s(?:\w+:)?(?:initials|userId|providerId))="[^"]*"`)
	// Nome em autores de comentário do PowerPoint (<p:cmAuthor name=…>) e pessoas.
	reNomeDeAutor = regexp.MustCompile(`(<(?:\w+:)?(?:cmAuthor|author|person)\s[^>]*?)\bname="[^"]*"`)
	// Datas de comentários e alterações: w:date, dT (Excel), dt e created (PowerPoint).
	reDataAtributo = regexp.MustCompile(`(\s(?:\w+:)?(?:date|dateUtc|dT|dt|created))="[^"]*"`)
	// <author>Fulano</author> nos comentários do Excel e <dc:creator> no LibreOffice.
	reAutorElemento = regexp.MustCompile(`(<(?:\w+:)?(?:author|creator)>)[^<]*(</(?:\w+:)?(?:author|creator)>)`)
	// Iniciais de quem comentou no LibreOffice (<meta:creator-initials>, <loext:sender-initials>).
	reIniciaisElemento = regexp.MustCompile(`(<(?:\w+:)?(?:creator-initials|sender-initials)>)[^<]*(</(?:\w+:)?(?:creator-initials|sender-initials)>)`)
	reDataElemento     = regexp.MustCompile(`(<(?:\w+:)?date>)[^<]*(</(?:\w+:)?date>)`)
	// Último local onde a planilha foi salva (<x15ac:absPath url="C:\Users\…">).
	reCaminhoSalvo = regexp.MustCompile(`(<(?:\w+:)?absPath\s[^>]*?)\burl="[^"]*"`)
	// Vínculos para arquivos do computador nos .rels (o modelo Normal.dotm, outras planilhas).
	reVinculoLocal = regexp.MustCompile(`(?i)(\sTarget=")(?:file:[^"]*|[a-z]:\\[^"]*|\\\\[^"]*)"`)
	// Impressora no settings.xml do LibreOffice (nome e configuração em base64).
	reImpressora = regexp.MustCompile(`(<config:config-item config:name="(?:PrinterName|PrinterSetup)"[^>]*>)[^<]*(</config:config-item>)`)
	reVersaoODF  = regexp.MustCompile(`office:version="([0-9.]+)"`)
)

const (
	coreVazio = `<?xml version="1.0" encoding="UTF-8" standalone="yes"?>
<cp:coreProperties xmlns:cp="http://schemas.openxmlformats.org/package/2006/metadata/core-properties" xmlns:dc="http://purl.org/dc/elements/1.1/" xmlns:dcterms="http://purl.org/dc/terms/" xmlns:dcmitype="http://purl.org/dc/dcmitype/" xmlns:xsi="http://www.w3.org/2001/XMLSchema-instance"></cp:coreProperties>`
	appVazio = `<?xml version="1.0" encoding="UTF-8" standalone="yes"?>
<Properties xmlns="http://schemas.openxmlformats.org/officeDocument/2006/extended-properties" xmlns:vt="http://schemas.openxmlformats.org/officeDocument/2006/docPropsVTypes"></Properties>`
	customVazio = `<?xml version="1.0" encoding="UTF-8" standalone="yes"?>
<Properties xmlns="http://schemas.openxmlformats.org/officeDocument/2006/custom-properties" xmlns:vt="http://schemas.openxmlformats.org/officeDocument/2006/docPropsVTypes"></Properties>`
	metaODFVazio = `<?xml version="1.0" encoding="UTF-8"?>
<office:document-meta xmlns:office="urn:oasis:names:tc:opendocument:xmlns:office:1.0" office:version="%s"><office:meta/></office:document-meta>`
)

// Office regrava um documento do Office ou do LibreOffice sem os metadados. ErrFormato
// se o ZIP não é um deles (um .zip comum fica como veio).
func Office(dados []byte) ([]byte, error) {
	zr, err := zip.NewReader(bytes.NewReader(dados), int64(len(dados)))
	if err != nil {
		return nil, fmt.Errorf("%w: %w", ErrFormato, err)
	}
	if !ehOffice(zr) {
		return nil, fmt.Errorf("%w: ZIP que não é documento", ErrFormato)
	}
	var out bytes.Buffer
	zw := zip.NewWriter(&out)
	aberto := int64(0)
	for _, f := range zr.File {
		conteudo, err := lerDoZip(f, maxDescompactado-aberto)
		if err != nil {
			return nil, err
		}
		aberto += int64(len(conteudo))
		if err := gravarNoZip(zw, f, limparParte(f.Name, conteudo)); err != nil {
			return nil, err
		}
	}
	if err := zw.Close(); err != nil {
		return nil, err
	}
	return out.Bytes(), nil
}

func ehOffice(zr *zip.Reader) bool {
	for _, f := range zr.File {
		if f.Name == "[Content_Types].xml" {
			return true
		}
		if f.Name == "mimetype" {
			c, err := lerDoZip(f, 200)
			return err == nil && bytes.HasPrefix(c, []byte("application/vnd.oasis.opendocument."))
		}
	}
	return false
}

func lerDoZip(f *zip.File, limite int64) ([]byte, error) {
	rc, err := f.Open()
	if err != nil {
		return nil, fmt.Errorf("%w: %s: %w", ErrFormato, f.Name, err)
	}
	defer rc.Close()
	b, err := io.ReadAll(io.LimitReader(rc, limite+1))
	if err != nil {
		return nil, fmt.Errorf("%w: %s: %w", ErrFormato, f.Name, err)
	}
	if int64(len(b)) > limite {
		return nil, fmt.Errorf("%w: documento grande demais aberto", ErrFormato)
	}
	return b, nil
}

// gravarNoZip grava com cabeçalho novo: só nome e método. A data de cada arquivo fica
// zerada (1980), o comentário e os campos extras somem. O "mimetype" do LibreOffice
// precisa continuar sem compressão e sem descritor no fim, então o que estava sem
// compressão é gravado cru, com CRC e tamanhos calculados aqui.
func gravarNoZip(zw *zip.Writer, f *zip.File, conteudo []byte) error {
	h := &zip.FileHeader{Name: f.Name, Method: f.Method}
	if f.Method == zip.Store {
		h.CRC32 = crc32.ChecksumIEEE(conteudo)
		h.CompressedSize64 = uint64(len(conteudo))
		h.UncompressedSize64 = uint64(len(conteudo))
		w, err := zw.CreateRaw(h)
		if err != nil {
			return err
		}
		_, err = w.Write(conteudo)
		return err
	}
	h.Method = zip.Deflate
	w, err := zw.CreateHeader(h)
	if err != nil {
		return err
	}
	_, err = w.Write(conteudo)
	return err
}

func limparParte(nome string, c []byte) []byte {
	switch nome {
	case "docProps/core.xml":
		return []byte(coreVazio)
	case "docProps/app.xml":
		return []byte(appVazio)
	case "docProps/custom.xml":
		return []byte(customVazio)
	case "meta.xml":
		versao := "1.2"
		if m := reVersaoODF.FindSubmatch(c); m != nil {
			versao = string(m[1])
		}
		return fmt.Appendf(nil, metaODFVazio, versao)
	}
	switch {
	case bytes.HasPrefix(c, []byte{0xFF, 0xD8}):
		if limpo, err := JPEG(c); err == nil {
			return limpo
		}
	case bytes.HasPrefix(c, []byte("\x89PNG")):
		if limpo, err := PNG(c); err == nil {
			return limpo
		}
	case strings.HasSuffix(nome, ".rels"):
		return reVinculoLocal.ReplaceAll(c, []byte(`${1}"`))
	case strings.HasSuffix(nome, ".xml"):
		return limparXML(c)
	}
	return c
}

func limparXML(c []byte) []byte {
	c = reAutorAtributo.ReplaceAll(c, []byte(`${1}="`+autorAnonimo+`"`))
	c = reIdAtributo.ReplaceAll(c, []byte(`${1}=""`))
	c = reNomeDeAutor.ReplaceAll(c, []byte(`${1}name="`+autorAnonimo+`"`))
	c = reDataAtributo.ReplaceAll(c, []byte(`${1}="`+dataFixa+`"`))
	c = reAutorElemento.ReplaceAll(c, []byte(`${1}`+autorAnonimo+`${2}`))
	c = reIniciaisElemento.ReplaceAll(c, []byte(`${1}${2}`))
	c = reDataElemento.ReplaceAll(c, []byte(`${1}`+dataFixa+`${2}`))
	c = reCaminhoSalvo.ReplaceAll(c, []byte(`${1}url=""`))
	return reImpressora.ReplaceAll(c, []byte(`${1}${2}`))
}
