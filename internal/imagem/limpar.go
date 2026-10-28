// Package imagem re-codifica as imagens das denúncias e apaga os metadados.
//
// Foto de celular carrega EXIF: modelo do aparelho, GPS, hora em que foi tirada,
// às vezes o nome do dono. Qualquer um deles aponta quem denunciou. Aqui a imagem
// é decodificada até virar só pixels e gravada como um JPEG novo: o encoder da
// biblioteca padrão não escreve EXIF, XMP, ICC nem comentário. A única coisa lida
// do EXIF antes de jogá-lo fora é a orientação, aplicada nos pixels para a foto
// em pé não ficar deitada.
package imagem

import (
	"bytes"
	"errors"
	"image"
	"image/color"
	"image/jpeg"
	_ "image/png"

	"golang.org/x/image/draw"
	_ "golang.org/x/image/webp"
)

var (
	ErrFormato      = errors.New("só imagens JPG, PNG ou WebP")
	ErrGrandeDemais = errors.New("imagem grande demais")
)

var formatoAceito = map[string]bool{"jpeg": true, "png": true, "webp": true}

const (
	// maxPixelsEntrada barra a "bomba": um PNG de poucos KB pode declarar
	// 100 mil × 100 mil pixels e estourar a memória ao decodificar.
	maxPixelsEntrada = 40_000_000
	ladoMax          = 2000
	qualidade        = 85
)

// Limpar devolve a imagem como JPEG novo, sem metadados, com no máximo 2000 px de lado.
func Limpar(entrada []byte) ([]byte, error) {
	// O formato é conferido aqui, e não só pelos decoders registrados: o registro
	// vale para o binário inteiro, e um import de image/gif em qualquer pacote
	// passaria a aceitar GIF.
	cfg, formato, err := image.DecodeConfig(bytes.NewReader(entrada))
	if err != nil || !formatoAceito[formato] {
		return nil, ErrFormato
	}
	if cfg.Width <= 0 || cfg.Height <= 0 || cfg.Width*cfg.Height > maxPixelsEntrada {
		return nil, ErrGrandeDemais
	}
	img, _, err := image.Decode(bytes.NewReader(entrada))
	if err != nil {
		return nil, ErrFormato
	}
	if formato == "jpeg" {
		img = orientar(img, orientacaoExif(entrada))
	}

	var saida bytes.Buffer
	if err := jpeg.Encode(&saida, reduzirSobreBranco(img), &jpeg.Options{Quality: qualidade}); err != nil {
		return nil, err
	}
	return saida.Bytes(), nil
}

// reduzirSobreBranco cabe a imagem em ladoMax × ladoMax e pinta a transparência de
// branco (JPEG não tem canal alfa; sem isso, o transparente sairia preto).
func reduzirSobreBranco(img image.Image) *image.RGBA {
	b := img.Bounds()
	w, h := b.Dx(), b.Dy()
	if maior := max(w, h); maior > ladoMax {
		w, h = max(1, w*ladoMax/maior), max(1, h*ladoMax/maior)
	}
	dst := image.NewRGBA(image.Rect(0, 0, w, h))
	draw.Draw(dst, dst.Bounds(), image.NewUniform(color.White), image.Point{}, draw.Src)
	draw.CatmullRom.Scale(dst, dst.Bounds(), img, b, draw.Over, nil)
	return dst
}
