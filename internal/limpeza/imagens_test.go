package limpeza

import (
	"bytes"
	"encoding/binary"
	"errors"
	"hash/crc32"
	"image"
	"image/color"
	"image/color/palette"
	"image/gif"
	"image/jpeg"
	"image/png"
	"testing"
)

func TestIdentificar(t *testing.T) {
	for nome, c := range map[string]struct {
		b    []byte
		quer Formato
	}{
		"mp4":   {cx("ftyp", []byte("isom")), FormatoMP4},
		"mov":   {cx("wide", make([]byte, 8)), FormatoMP4},
		"mp3":   {[]byte("ID3\x03\x00\x00\x00\x00\x00\x00"), FormatoMP3},
		"gif":   {[]byte("GIF89a......"), FormatoGIF},
		"zip":   {[]byte("PK\x03\x04........"), FormatoZip},
		"pdf":   {[]byte("%PDF-1.7\n...."), FormatoPDF},
		"texto": {[]byte("só um texto qualquer"), Nenhum},
		"curto": {[]byte("ab"), Nenhum},
	} {
		if got := Identificar(c.b); got != c.quer {
			t.Errorf("%s: %v", nome, got)
		}
	}
}

// --- MP3 ---

func id3(corpo []byte, flags byte) []byte {
	h := []byte{'I', 'D', '3', 3, 0, flags}
	n := len(corpo)
	return append(append(h, byte(n>>21&0x7f), byte(n>>14&0x7f), byte(n>>7&0x7f), byte(n&0x7f)), corpo...)
}

func TestMP3EsvaziaAsEtiquetas(t *testing.T) {
	audio := bytes.Repeat([]byte{0xFF, 0xFB, 0x90, 0x00}, 100)
	v1 := append([]byte("TAG"), bytes.Repeat([]byte("F"), 125)...)
	copy(v1[3:], "Fulano de Tal")
	b := append(append(id3([]byte("TPE1\x00\x00\x00\x07\x00\x00\x00Fulano"), 0), audio...), v1...)

	rs, err := RemendosMP3(bytes.NewReader(b), int64(len(b)))
	if err != nil {
		t.Fatal(err)
	}
	limpo := aplicar(b, rs)
	if bytes.Contains(limpo, []byte("Fulano")) || !bytes.Contains(limpo, audio) {
		t.Error("não esvaziou as etiquetas ou mexeu no áudio")
	}
	if !bytes.HasPrefix(limpo[len(limpo)-128:], []byte("TAG\x00")) || limpo[len(limpo)-1] != 255 {
		t.Error("ID3v1 não virou etiqueta vazia")
	}
}

func TestMP3ComRodapeESemID3v1(t *testing.T) {
	corpo := []byte("TPE1 Fulano")
	b := append(append(id3(corpo, 0x10), []byte("3DI\x04\x00\x10\x00\x00\x00\x0b")...), 0xFF, 0xFB)
	rs, err := RemendosMP3(bytes.NewReader(b), int64(len(b)))
	if err != nil {
		t.Fatal(err)
	}
	limpo := aplicar(b, rs)
	if bytes.Contains(limpo, []byte("Fulano")) || bytes.Contains(limpo, []byte("3DI")) || len(rs) != 2 {
		t.Errorf("rodapé: %q", limpo)
	}
}

func TestMP3Recusa(t *testing.T) {
	ruim := id3([]byte("x"), 0)
	ruim[9] = 0x80 // byte de tamanho com o bit alto: não é syncsafe
	for nome, b := range map[string][]byte{
		"sem ID3":               []byte("RIFF....WAVEfmt "),
		"tamanho inválido":      ruim,
		"etiqueta passa do fim": id3(make([]byte, 50), 0)[:30],
	} {
		if _, err := RemendosMP3(bytes.NewReader(b), int64(len(b))); !errors.Is(err, ErrFormato) {
			t.Errorf("%s: %v", nome, err)
		}
	}
}

// --- JPEG e PNG de dentro de documentos ---

func pixels() image.Image {
	img := image.NewRGBA(image.Rect(0, 0, 16, 16))
	for i := range img.Pix {
		img.Pix[i] = byte(i)
	}
	return img
}

func segmento(m byte, corpo string) []byte {
	return append([]byte{0xFF, m, byte((len(corpo) + 2) >> 8), byte(len(corpo) + 2)}, corpo...)
}

func jpegComMetadados(t *testing.T) (original, comMeta []byte) {
	t.Helper()
	var b bytes.Buffer
	if err := jpeg.Encode(&b, pixels(), nil); err != nil {
		t.Fatal(err)
	}
	original = b.Bytes()
	meta := bytes.Join([][]byte{
		segmento(0xE0, "JFIF\x00\x01\x01"),
		segmento(0xE1, "Exif\x00\x00Fulano GPS"),
		segmento(0xE2, "ICC_PROFILE\x00perfil"),
		segmento(0xE2, "MPF\x00Fulano"),
		segmento(0xED, "Photoshop 3.0 Fulano"),
		segmento(0xEE, "Adobe\x00cores"),
		segmento(0xFE, "comentário do Fulano"),
		{0xFF}, // byte de preenchimento antes do próximo marcador
	}, nil)
	comMeta = append(append(append([]byte{}, original[:2]...), meta...), original[2:]...)
	comMeta = append(comMeta, []byte("miniatura do Fulano depois do fim")...)
	return original, comMeta
}

func TestJPEGSemPerda(t *testing.T) {
	original, comMeta := jpegComMetadados(t)
	limpo, err := JPEG(comMeta)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(limpo, []byte("Fulano")) {
		t.Error("sobrou metadado")
	}
	for _, fica := range []string{"JFIF", "ICC_PROFILE", "Adobe"} {
		if !bytes.Contains(limpo, []byte(fica)) {
			t.Errorf("tirou %s, que a cor precisa", fica)
		}
	}
	a, _ := jpeg.Decode(bytes.NewReader(original))
	b, err := jpeg.Decode(bytes.NewReader(limpo))
	if err != nil || !bytes.Equal(a.(*image.YCbCr).Y, b.(*image.YCbCr).Y) {
		t.Errorf("os pixels mudaram: %v", err)
	}
}

func TestJPEGRecusa(t *testing.T) {
	_, comMeta := jpegComMetadados(t)
	semFim := bytes.ReplaceAll(comMeta, []byte{0xFF, 0xD9}, []byte{0xFF, 0x00})
	for nome, b := range map[string][]byte{
		"não é JPEG":                []byte("GIF89a"),
		"cortado":                   comMeta[:30],
		"segmento inválido":         {0xFF, 0xD8, 0xFF, 0xE1, 0x00, 0x01},
		"sem fim":                   semFim,
		"lixo no lugar de marcador": {0xFF, 0xD8, 0x00, 0x00, 0x00, 0x00},
	} {
		if _, err := JPEG(b); !errors.Is(err, ErrFormato) {
			t.Errorf("%s: %v", nome, err)
		}
	}
}

func pedacoPNG(tipo string, dados []byte) []byte {
	b := binary.BigEndian.AppendUint32(nil, uint32(len(dados)))
	b = append(append(b, tipo...), dados...)
	return binary.BigEndian.AppendUint32(b, crc32.ChecksumIEEE(b[4:]))
}

func pngComTexto(t *testing.T) []byte {
	t.Helper()
	var b bytes.Buffer
	png.Encode(&b, pixels())
	p := b.Bytes()
	extra := append(pedacoPNG("tEXt", []byte("Author\x00Fulano")), pedacoPNG("tIME", make([]byte, 7))...)
	extra = append(extra, pedacoPNG("pHYs", make([]byte, 9))...)
	p = append(append(append([]byte{}, p[:33]...), extra...), p[33:]...) // depois do IHDR
	return append(p, "lixo do Fulano depois do IEND"...)
}

func TestPNGSemPerda(t *testing.T) {
	limpo, err := PNG(pngComTexto(t))
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(limpo, []byte("Fulano")) || bytes.Contains(limpo, []byte("tIME")) || !bytes.Contains(limpo, []byte("pHYs")) {
		t.Error("pedaços errados")
	}
	if _, err := png.Decode(bytes.NewReader(limpo)); err != nil {
		t.Error(err)
	}
	if _, err := PNG([]byte("não é png")); !errors.Is(err, ErrFormato) {
		t.Error("aceitou o que não é PNG")
	}
	if _, err := PNG(pngComTexto(t)[:60]); !errors.Is(err, ErrFormato) {
		t.Error("aceitou PNG cortado")
	}
}

// --- GIF ---

func gifComComentario(t *testing.T, quadros, lado int) []byte {
	t.Helper()
	g := &gif.GIF{}
	for i := range quadros {
		q := image.NewPaletted(image.Rect(0, 0, lado, lado), palette.Plan9)
		q.SetColorIndex(i%lado, 0, uint8(i))
		g.Image = append(g.Image, q)
		g.Delay = append(g.Delay, 10)
	}
	var b bytes.Buffer
	if err := gif.EncodeAll(&b, g); err != nil {
		t.Fatal(err)
	}
	c := b.Bytes()
	i := 13 + tabelaDeCores(c[10])
	comentario := append([]byte{0x21, 0xFE, 6}, "Fulano"...)
	comentario = append(comentario, 0)
	return append(append(append([]byte{}, c[:i]...), comentario...), c[i:]...)
}

func TestGIFTiraComentario(t *testing.T) {
	b := gifComComentario(t, 3, 8)
	if Identificar(b) != FormatoGIF {
		t.Fatal("não identificou")
	}
	limpo, err := GIF(b)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(limpo, []byte("Fulano")) {
		t.Error("sobrou o comentário")
	}
	g, err := gif.DecodeAll(bytes.NewReader(limpo))
	if err != nil || len(g.Image) != 3 || g.Image[2].At(2, 0) != color.Color(palette.Plan9[2]) {
		t.Errorf("animação mudou: %v", err)
	}
}

func TestGIFRecusa(t *testing.T) {
	b := gifComComentario(t, 1, 8)
	quadroGigante := append([]byte{}, b...)
	i := bytes.Index(quadroGigante, []byte{0x2C, 0, 0, 0, 0}) // descritor do quadro
	binary.LittleEndian.PutUint16(quadroGigante[i+5:], 20000)
	binary.LittleEndian.PutUint16(quadroGigante[i+7:], 20000)
	for nome, g := range map[string][]byte{
		"curto":          []byte("GIF89a"),
		"bloco estranho": append(append([]byte{}, b[:13+tabelaDeCores(b[10])]...), 0x99),
		"sem fim":        b[:len(b)-1],
		"quadro cortado": b[:i+4],
		"quadro gigante": quadroGigante,
		"não decodifica": append(append([]byte{}, b[:i+10+tabelaDeCores(b[i+9])]...), 0x02, 0x01, 0xFF, 0x00, 0x3B),
	} {
		if _, err := GIF(g); !errors.Is(err, ErrFormato) {
			t.Errorf("%s: %v", nome, err)
		}
	}
}
