package limpeza

import (
	"bytes"
	"fmt"
	"io"
)

// MP3: os metadados ficam em etiquetas ID3 (artista, programa que gravou, data,
// comentário, às vezes uma foto). A ID3v2 abre o arquivo; a ID3v1 são os últimos 128
// bytes, começando por "TAG".
//
// A ID3v2 continua no lugar, só que vazia: cabeçalho sem flags e o corpo inteiro
// zerado, que a especificação chama de preenchimento (padding). A ID3v1 vira uma
// etiqueta vazia. O áudio não é tocado.

// RemendosMP3 devolve os remendos que esvaziam as etiquetas. ErrFormato se não
// começa com uma ID3v2 válida.
func RemendosMP3(r io.ReaderAt, tamanho int64) ([]Remendo, error) {
	var h [10]byte
	if _, err := r.ReadAt(h[:], 0); err != nil || !bytes.Equal(h[:3], []byte("ID3")) {
		return nil, fmt.Errorf("%w: sem etiqueta ID3", ErrFormato)
	}
	corpo, ok := syncsafe(h[6:10])
	fim := 10 + corpo
	if h[5]&0x10 != 0 { // rodapé de 10 bytes depois do corpo
		fim += 10
	}
	if !ok || fim > tamanho {
		return nil, fmt.Errorf("%w: etiqueta ID3 com tamanho inválido", ErrFormato)
	}
	out := []Remendo{
		{Pos: 5, Dados: []byte{0}},
		{Pos: 10, Dados: make([]byte, fim-10)},
	}

	if tamanho-fim >= 128 {
		var v1 [3]byte
		if _, err := r.ReadAt(v1[:], tamanho-128); err == nil && string(v1[:]) == "TAG" {
			vazia := make([]byte, 128)
			copy(vazia, "TAG")
			vazia[127] = 255 // gênero "nenhum"
			out = append(out, Remendo{Pos: tamanho - 128, Dados: vazia})
		}
	}
	return out, nil
}

// syncsafe lê o tamanho da ID3v2: 4 bytes com 7 bits úteis cada.
func syncsafe(b []byte) (int64, bool) {
	var n int64
	for _, x := range b {
		if x&0x80 != 0 {
			return 0, false
		}
		n = n<<7 | int64(x)
	}
	return n, true
}
