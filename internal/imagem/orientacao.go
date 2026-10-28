package imagem

import (
	"encoding/binary"
	"image"
)

const tagOrientacao = 0x0112

// orientacaoExif lê só a tag Orientation (0x0112) do IFD0 do EXIF de um JPEG.
// Qualquer coisa fora do esperado devolve 1 (sem girar): na dúvida, a foto sai
// como veio, nunca dá erro.
func orientacaoExif(jpg []byte) int {
	tiff := segmentoExif(jpg)
	if len(tiff) < 8 {
		return 1
	}
	var ordem binary.ByteOrder
	switch string(tiff[:2]) {
	case "II":
		ordem = binary.LittleEndian
	case "MM":
		ordem = binary.BigEndian
	default:
		return 1
	}
	ifd := int(ordem.Uint32(tiff[4:8]))
	if ifd < 8 || ifd+2 > len(tiff) {
		return 1
	}
	n := int(ordem.Uint16(tiff[ifd:]))
	for i := range n {
		e := ifd + 2 + i*12
		if e+12 > len(tiff) {
			return 1
		}
		if ordem.Uint16(tiff[e:]) == tagOrientacao {
			if v := int(ordem.Uint16(tiff[e+8:])); v >= 1 && v <= 8 {
				return v
			}
			return 1
		}
	}
	return 1
}

// segmentoExif devolve o bloco TIFF do APP1 "Exif\0\0", ou nil. Para no início
// dos dados da imagem (SOS): o EXIF sempre vem antes.
func segmentoExif(jpg []byte) []byte {
	if len(jpg) < 4 || jpg[0] != 0xFF || jpg[1] != 0xD8 {
		return nil
	}
	for i := 2; i+4 <= len(jpg); {
		if jpg[i] != 0xFF {
			return nil
		}
		marcador := jpg[i+1]
		tam := int(binary.BigEndian.Uint16(jpg[i+2:]))
		fim := i + 2 + tam
		if marcador == 0xDA || tam < 2 || fim > len(jpg) {
			return nil
		}
		dados := jpg[i+4 : fim]
		if marcador == 0xE1 && len(dados) >= 6 && string(dados[:6]) == "Exif\x00\x00" {
			return dados[6:]
		}
		i = fim
	}
	return nil
}

// orientar aplica nos pixels a transformação que a tag EXIF pede (1 a 8).
func orientar(img image.Image, o int) image.Image {
	if o <= 1 || o > 8 {
		return img
	}
	b := img.Bounds()
	w, h := b.Dx(), b.Dy()
	dw, dh := w, h
	if o >= 5 { // 5 a 8 trocam largura e altura
		dw, dh = h, w
	}
	dst := image.NewRGBA(image.Rect(0, 0, dw, dh))
	for y := range dh {
		for x := range dw {
			sx, sy := origem(o, x, y, w, h)
			dst.Set(x, y, img.At(b.Min.X+sx, b.Min.Y+sy))
		}
	}
	return dst
}

// origem diz de que pixel da imagem original (w × h) vem o pixel (x, y) do resultado.
func origem(o, x, y, w, h int) (int, int) {
	switch o {
	case 2: // espelhada na horizontal
		return w - 1 - x, y
	case 3: // girada 180°
		return w - 1 - x, h - 1 - y
	case 4: // espelhada na vertical
		return x, h - 1 - y
	case 5: // transposta
		return y, x
	case 6: // girar 90° no sentido horário
		return y, h - 1 - x
	case 7: // transversa
		return w - 1 - y, h - 1 - x
	default: // 8: girar 90° no sentido anti-horário
		return w - 1 - y, x
	}
}
