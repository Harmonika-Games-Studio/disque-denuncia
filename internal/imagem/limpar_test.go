package imagem

import (
	"bytes"
	"encoding/binary"
	"errors"
	"hash/crc32"
	"image"
	"image/color"
	"image/gif"
	"image/jpeg"
	"image/png"
	"testing"
)

// foto monta uma imagem w × h azul com o quadrante de cima à esquerda vermelho.
func foto(w, h int) *image.RGBA {
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	for y := range h {
		for x := range w {
			c := color.RGBA{0, 0, 255, 255}
			if x < w/2 && y < h/2 {
				c = color.RGBA{255, 0, 0, 255}
			}
			img.Set(x, y, c)
		}
	}
	return img
}

func codificarJPEG(t *testing.T, img image.Image) []byte {
	t.Helper()
	var b bytes.Buffer
	if err := jpeg.Encode(&b, img, nil); err != nil {
		t.Fatal(err)
	}
	return b.Bytes()
}

// comExif põe logo depois do SOI um APP1 com IFD0 = {Orientation, Artist}.
func comExif(jpg []byte, ordem binary.ByteOrder, orientacao uint16, artista string) []byte {
	art := append([]byte(artista), 0)
	tiff := make([]byte, 8+2+2*12+4)
	if ordem == binary.LittleEndian {
		copy(tiff, "II")
	} else {
		copy(tiff, "MM")
	}
	ordem.PutUint16(tiff[2:], 42)
	ordem.PutUint32(tiff[4:], 8)
	ordem.PutUint16(tiff[8:], 2)
	e := tiff[10:]
	ordem.PutUint16(e[0:], 0x0112) // Orientation, SHORT, 1
	ordem.PutUint16(e[2:], 3)
	ordem.PutUint32(e[4:], 1)
	ordem.PutUint16(e[8:], orientacao)
	ordem.PutUint16(e[12:], 0x013B) // Artist, ASCII, aponta para depois do IFD
	ordem.PutUint16(e[14:], 2)
	ordem.PutUint32(e[16:], uint32(len(art)))
	ordem.PutUint32(e[20:], uint32(len(tiff)))
	tiff = append(tiff, art...)

	dados := append([]byte("Exif\x00\x00"), tiff...)
	seg := []byte{0xFF, 0xE1, 0, 0}
	binary.BigEndian.PutUint16(seg[2:], uint16(len(dados)+2))
	seg = append(seg, dados...)
	out := append([]byte{}, jpg[:2]...)
	out = append(out, seg...)
	return append(out, jpg[2:]...)
}

func decodificar(t *testing.T, b []byte) image.Image {
	t.Helper()
	img, formato, err := image.Decode(bytes.NewReader(b))
	if err != nil || formato != "jpeg" {
		t.Fatalf("saída não é JPEG: %s %v", formato, err)
	}
	return img
}

func vermelho(c color.Color) bool {
	r, g, b, _ := c.RGBA()
	return r > 0xC000 && g < 0x4000 && b < 0x4000
}

func TestLimparApagaExif(t *testing.T) {
	entrada := comExif(codificarJPEG(t, foto(40, 20)), binary.LittleEndian, 1, "Fulano de Tal")
	if !bytes.Contains(entrada, []byte("Fulano de Tal")) {
		t.Fatal("o EXIF de teste não entrou")
	}
	saida, err := Limpar(entrada)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(saida, []byte("Fulano de Tal")) || bytes.Contains(saida, []byte("Exif")) {
		t.Error("metadado sobreviveu")
	}
	if segmentoExif(saida) != nil {
		t.Error("saída ainda tem APP1 Exif")
	}
	if b := decodificar(t, saida).Bounds(); b.Dx() != 40 || b.Dy() != 20 {
		t.Errorf("tamanho %v", b)
	}
}

func TestLimparAplicaOrientacao(t *testing.T) {
	// O quadrante vermelho (canto de cima à esquerda) vai para onde cada orientação manda.
	for _, tc := range []struct {
		o          uint16
		w, h, x, y int
	}{
		{1, 40, 20, 5, 5},
		{2, 40, 20, 35, 5},
		{3, 40, 20, 35, 15},
		{4, 40, 20, 5, 15},
		{5, 20, 40, 5, 5},
		{6, 20, 40, 15, 5},
		{7, 20, 40, 15, 35},
		{8, 20, 40, 5, 35},
	} {
		for _, ordem := range []binary.ByteOrder{binary.LittleEndian, binary.BigEndian} {
			saida, err := Limpar(comExif(codificarJPEG(t, foto(40, 20)), ordem, tc.o, "x"))
			if err != nil {
				t.Fatal(err)
			}
			img := decodificar(t, saida)
			if b := img.Bounds(); b.Dx() != tc.w || b.Dy() != tc.h {
				t.Errorf("orientação %d: tamanho %v, esperava %dx%d", tc.o, b, tc.w, tc.h)
			}
			if !vermelho(img.At(tc.x, tc.y)) {
				t.Errorf("orientação %d (%v): (%d,%d) não é vermelho", tc.o, ordem, tc.x, tc.y)
			}
		}
	}
}

func TestLimparReduzEConvertePNG(t *testing.T) {
	var b bytes.Buffer
	png.Encode(&b, image.NewRGBA(image.Rect(0, 0, 3000, 1500))) // todo transparente
	saida, err := Limpar(b.Bytes())
	if err != nil {
		t.Fatal(err)
	}
	img := decodificar(t, saida)
	if bb := img.Bounds(); bb.Dx() != 2000 || bb.Dy() != 1000 {
		t.Errorf("tamanho %v", bb)
	}
	if r, g, bl, _ := img.At(10, 10).RGBA(); r < 0xF000 || g < 0xF000 || bl < 0xF000 {
		t.Error("transparente devia virar branco")
	}
}

func TestLimparRecusaFormatoDesconhecido(t *testing.T) {
	var g bytes.Buffer
	gif.Encode(&g, foto(4, 4), nil)
	for nome, entrada := range map[string][]byte{"texto": []byte("não sou imagem"), "gif": g.Bytes()} {
		if _, err := Limpar(entrada); !errors.Is(err, ErrFormato) {
			t.Errorf("%s: %v", nome, err)
		}
	}
}

func TestLimparRecusaJPEGCortado(t *testing.T) {
	jpg := codificarJPEG(t, foto(40, 20))
	if _, err := Limpar(jpg[:len(jpg)/2]); !errors.Is(err, ErrFormato) {
		t.Errorf("veio %v", err)
	}
}

// Um PNG que declara 10000 × 10000 é recusado só pelo cabeçalho, sem decodificar.
func TestLimparRecusaBomba(t *testing.T) {
	ihdr := make([]byte, 13)
	binary.BigEndian.PutUint32(ihdr[0:], 10000)
	binary.BigEndian.PutUint32(ihdr[4:], 10000)
	ihdr[8], ihdr[9] = 8, 2 // 8 bits, RGB
	chunk := append([]byte("IHDR"), ihdr...)
	b := []byte("\x89PNG\r\n\x1a\n")
	b = binary.BigEndian.AppendUint32(b, 13)
	b = append(b, chunk...)
	b = binary.BigEndian.AppendUint32(b, crc32.ChecksumIEEE(chunk))
	if _, err := Limpar(b); !errors.Is(err, ErrGrandeDemais) {
		t.Errorf("veio %v", err)
	}
}

func TestOrientacaoExifTolerante(t *testing.T) {
	jpg := codificarJPEG(t, foto(4, 4))
	for nome, entrada := range map[string][]byte{
		"vazio":             nil,
		"não é JPEG":        []byte("abcdef"),
		"sem EXIF":          jpg,
		"orientação 9":      comExif(jpg, binary.LittleEndian, 9, "x"),
		"ordem inválida":    bytes.Replace(comExif(jpg, binary.LittleEndian, 6, "x"), []byte("II*"), []byte("XX*"), 1),
		"segmento cortado":  comExif(jpg, binary.LittleEndian, 6, "x")[:30],
		"marcador quebrado": append([]byte{0xFF, 0xD8, 0x00}, jpg[3:]...),
	} {
		if o := orientacaoExif(entrada); o != 1 {
			t.Errorf("%s: %d", nome, o)
		}
	}
}

func TestOrientacaoExifIFDForaDoBloco(t *testing.T) {
	jpg := comExif(codificarJPEG(t, foto(4, 4)), binary.LittleEndian, 6, "x")
	i := bytes.Index(jpg, []byte("II*\x00"))
	binary.LittleEndian.PutUint32(jpg[i+4:], 60000) // IFD0 aponta para fora
	if o := orientacaoExif(jpg); o != 1 {
		t.Errorf("IFD fora: %d", o)
	}
	jpg = comExif(codificarJPEG(t, foto(4, 4)), binary.LittleEndian, 6, "x")
	i = bytes.Index(jpg, []byte("II*\x00"))
	binary.LittleEndian.PutUint16(jpg[i+8:], 500) // mais entradas do que cabem
	binary.LittleEndian.PutUint16(jpg[i+10:], 0x0100)
	if o := orientacaoExif(jpg); o != 1 {
		t.Errorf("entradas demais: %d", o)
	}
}

func TestOrientacaoExifSemTag(t *testing.T) {
	jpg := comExif(codificarJPEG(t, foto(4, 4)), binary.BigEndian, 6, "x")
	i := bytes.Index(jpg, []byte("MM\x00*"))
	binary.BigEndian.PutUint16(jpg[i+10:], 0x0100) // troca a tag Orientation por outra
	if o := orientacaoExif(jpg); o != 1 {
		t.Errorf("sem a tag: %d", o)
	}
}
