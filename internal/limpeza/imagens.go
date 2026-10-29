package limpeza

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"image/gif"
)

// Imagens de dentro de documentos e PDFs. Ali não dá para refazer a imagem como as
// fotos soltas (o tamanho e o formato precisam ficar os mesmos), então os segmentos
// de metadado são cortados e os pixels ficam intactos.

// JPEG tira APP1 (EXIF, XMP), APP13 (IPTC), os outros APPn e os comentários. Ficam
// APP0 (JFIF), APP2 só com perfil de cor ICC e APP14 (Adobe, que diz como ler as cores
// de um CMYK). O que vem depois do fim da imagem (miniaturas extras de alguns celulares)
// é descartado.
func JPEG(b []byte) ([]byte, error) {
	if !bytes.HasPrefix(b, []byte{0xFF, 0xD8}) {
		return nil, fmt.Errorf("%w: não é JPEG", ErrFormato)
	}
	out := []byte{0xFF, 0xD8}
	for i := 2; ; {
		if i+4 > len(b) || b[i] != 0xFF {
			return nil, fmt.Errorf("%w: JPEG cortado", ErrFormato)
		}
		m := b[i+1]
		if m == 0xFF { // byte de preenchimento antes de um marcador
			i++
			continue
		}
		if m == 0xDA { // começo da imagem comprimida: copia até o fim (EOI)
			fim, ok := fimDoJPEG(b, i)
			if !ok {
				return nil, fmt.Errorf("%w: JPEG sem fim", ErrFormato)
			}
			return append(out, b[i:fim]...), nil
		}
		n := int(binary.BigEndian.Uint16(b[i+2 : i+4]))
		if n < 2 || i+2+n > len(b) {
			return nil, fmt.Errorf("%w: segmento JPEG inválido", ErrFormato)
		}
		seg := b[i : i+2+n]
		if guardarSegmento(m, seg[4:]) {
			out = append(out, seg...)
		}
		i += 2 + n
	}
}

func guardarSegmento(m byte, corpo []byte) bool {
	switch {
	case m == 0xE0:
		return true
	case m == 0xE2:
		return bytes.HasPrefix(corpo, []byte("ICC_PROFILE\x00"))
	case m == 0xEE:
		return bytes.HasPrefix(corpo, []byte("Adobe"))
	case m >= 0xE1 && m <= 0xEF, m == 0xFE:
		return false
	}
	return true // tabelas, quadro, reinício: a imagem em si
}

// fimDoJPEG acha o EOI depois de SOS. Nos dados comprimidos, 0xFF só aparece seguido de
// 0x00 (byte escapado), de RSTn ou de outro marcador (as várias passadas de um JPEG
// progressivo); o primeiro FF D9 é o fim.
func fimDoJPEG(b []byte, de int) (int, bool) {
	for i := de; i+1 < len(b); i++ {
		if b[i] == 0xFF && b[i+1] == 0xD9 {
			return i + 2, true
		}
	}
	return 0, false
}

// pedaçosPNG que ficam: os que a imagem precisa para ser desenhada igual. Saem texto
// (tEXt, zTXt, iTXt: autor, programa, comentário), eXIf, tIME e os particulares.
var pedacosPNG = map[string]bool{
	"tRNS": true, "gAMA": true, "cHRM": true, "sRGB": true, "iCCP": true, "sBIT": true,
	"pHYs": true, "bKGD": true, "hIST": true, "sPLT": true, "cICP": true,
	"acTL": true, "fcTL": true, "fdAT": true, // PNG animado
}

// PNG tira os pedaços de metadado. Os críticos (IHDR, PLTE, IDAT, IEND: primeira letra
// maiúscula) ficam sempre; o que vem depois de IEND é descartado.
func PNG(b []byte) ([]byte, error) {
	assinatura := []byte("\x89PNG\r\n\x1a\n")
	if !bytes.HasPrefix(b, assinatura) {
		return nil, fmt.Errorf("%w: não é PNG", ErrFormato)
	}
	out := append([]byte{}, assinatura...)
	for i := len(assinatura); i+12 <= len(b); {
		n := int(binary.BigEndian.Uint32(b[i : i+4]))
		if i+12+n > len(b) {
			break
		}
		tipo := string(b[i+4 : i+8])
		if (tipo[0] >= 'A' && tipo[0] <= 'Z') || pedacosPNG[tipo] {
			out = append(out, b[i:i+12+n]...)
		}
		if tipo == "IEND" {
			return out, nil
		}
		i += 12 + n
	}
	return nil, fmt.Errorf("%w: PNG cortado", ErrFormato)
}

// maxPixelsGIF: soma de largura × altura de todos os quadros. Decodificar um GIF
// abre cada quadro na memória.
const maxPixelsGIF = 100_000_000

// GIF regrava a animação inteira: saem comentários e blocos de aplicação (XMP). Paleta,
// quadros, tempos e repetição ficam iguais.
func GIF(b []byte) ([]byte, error) {
	pixels, err := pixelsDoGIF(b)
	if err != nil {
		return nil, err
	}
	if pixels > maxPixelsGIF {
		return nil, fmt.Errorf("%w: GIF grande demais", ErrFormato)
	}
	g, err := gif.DecodeAll(bytes.NewReader(b))
	if err != nil {
		return nil, fmt.Errorf("%w: %w", ErrFormato, err)
	}
	var out bytes.Buffer
	if err := gif.EncodeAll(&out, g); err != nil {
		return nil, err
	}
	return out.Bytes(), nil
}

// pixelsDoGIF soma a área de todos os quadros lendo só os blocos, sem descomprimir: um
// GIF de poucos MB com milhares de quadros abriria gigabytes na memória.
func pixelsDoGIF(b []byte) (int, error) {
	invalido := fmt.Errorf("%w: GIF inválido", ErrFormato)
	if len(b) < 13 {
		return 0, invalido
	}
	i := 13 + tabelaDeCores(b[10])
	pixels := 0
	for i < len(b) {
		switch b[i] {
		case 0x3B: // fim
			return pixels, nil
		case 0x21: // extensão: rótulo e sub-blocos
			i += 2
		case 0x2C: // quadro: descritor, tabela local, tamanho do código LZW, sub-blocos
			if i+10 > len(b) {
				return 0, invalido
			}
			w := int(binary.LittleEndian.Uint16(b[i+5:]))
			h := int(binary.LittleEndian.Uint16(b[i+7:]))
			pixels += w * h
			i += 10 + tabelaDeCores(b[i+9]) + 1
		default:
			return 0, invalido
		}
		for i < len(b) && b[i] != 0 { // sub-blocos: tamanho + dados, até um zero
			i += 1 + int(b[i])
		}
		i++
	}
	return 0, invalido
}

// tabelaDeCores: bytes da tabela de cores que segue, pelas flags do descritor.
func tabelaDeCores(flags byte) int {
	if flags&0x80 == 0 {
		return 0
	}
	return 3 << (int(flags&7) + 1)
}
