package limpeza

import (
	"bytes"
	"encoding/binary"
	"errors"
	"io"
	"testing"
)

// caixa monta uma caixa ISO BMFF: tamanho (32 bits), tipo e corpo.
func cx(tipo string, corpo ...[]byte) []byte {
	c := bytes.Join(corpo, nil)
	out := binary.BigEndian.AppendUint32(nil, uint32(8+len(c)))
	return append(append(out, tipo...), c...)
}

// cabecalhoComDatas é mvhd/tkhd/mdhd: versão, flags e as duas datas (+ resto).
func cabecalhoComDatas(versao byte) []byte {
	b := []byte{versao, 0, 0, 0}
	data := []byte{0xDE, 0xAD, 0xBE, 0xEF}
	if versao == 1 {
		data = append(data, data...)
	}
	b = append(b, data...)
	b = append(b, data...)
	return append(b, make([]byte, 20)...) // timescale, duração etc.
}

func aplicar(b []byte, rs []Remendo) []byte {
	out := append([]byte{}, b...)
	for _, r := range rs {
		copy(out[r.Pos:], r.Dados)
	}
	return out
}

var midia = bytes.Repeat([]byte("quadro-de-video "), 64)

func videoDoCelular() []byte {
	return bytes.Join([][]byte{
		cx("ftyp", []byte("qt  \x00\x00\x02\x00qt  ")),
		cx("wide"),
		cx("moov",
			cx("mvhd", cabecalhoComDatas(0)),
			cx("trak",
				cx("tkhd", cabecalhoComDatas(1)),
				cx("mdia", cx("hdlr", []byte("vide")), cx("mdhd", cabecalhoComDatas(0))),
				cx("udta", cx("\xa9xyz", []byte("+23.5505-046.6333/"))),
			),
			cx("udta", cx("\xa9mak", []byte("Apple")), cx("\xa9mod", []byte("iPhone do Fulano")), []byte{0, 0, 0, 0}),
			cx("meta", []byte("com.apple.quicktime.location.ISO6709 +23.5505-046.6333/")),
			cx("uuid", []byte("XMP-uuid-16bytes<x:xmpmeta>Fulano</x:xmpmeta>")),
		),
		cx("uuid", []byte("PROFsony16bytes!GPS +23.5505")),
		cx("mdat", midia),
	}, nil)
}

func TestMP4ApagaGPSAparelhoEDatas(t *testing.T) {
	b := videoDoCelular()
	if Identificar(b) != FormatoMP4 {
		t.Fatal("não identificou")
	}
	rs, err := RemendosMP4(bytes.NewReader(b), int64(len(b)))
	if err != nil {
		t.Fatal(err)
	}
	limpo := aplicar(b, rs)
	if len(limpo) != len(b) {
		t.Fatal("mudou de tamanho")
	}
	for _, s := range []string{"23.5505", "Apple", "Fulano", "PROF", "\xde\xad\xbe\xef"} {
		if bytes.Contains(limpo, []byte(s)) {
			t.Errorf("sobrou %q", s)
		}
	}
	if !bytes.Contains(limpo, midia) {
		t.Error("mexeu no vídeo")
	}
	// Continua um arquivo de caixas válido, e limpar de novo não acha mais nada para apagar.
	rs2, err := RemendosMP4(bytes.NewReader(limpo), int64(len(limpo)))
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(aplicar(limpo, rs2), limpo) {
		t.Error("a segunda limpeza mudou o arquivo")
	}
}

func TestMP4TamanhosEspeciais(t *testing.T) {
	// Caixa com tamanho de 64 bits (size = 1) e mdat com size = 0 (vai até o fim).
	grande := append(binary.BigEndian.AppendUint32(nil, 1), "udta"...)
	corpo := []byte("Fulano")
	grande = binary.BigEndian.AppendUint64(grande, uint64(16+len(corpo)))
	grande = append(grande, corpo...)
	ateOFim := append(binary.BigEndian.AppendUint32(nil, 0), "mdat"...)
	b := bytes.Join([][]byte{cx("ftyp", []byte("isom")), cx("moov", cx("mvhd", cabecalhoComDatas(0))), grande, ateOFim, midia}, nil)

	rs, err := RemendosMP4(bytes.NewReader(b), int64(len(b)))
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(aplicar(b, rs), []byte("Fulano")) {
		t.Error("não apagou a caixa de 64 bits")
	}
}

func TestMP4Recusa(t *testing.T) {
	semMoov := bytes.Join([][]byte{cx("ftyp", []byte("heic")), cx("meta", []byte("exif")), cx("mdat", midia)}, nil)
	sobra := append(videoDoCelular(), 1, 2, 3)
	tamanhoErrado := videoDoCelular()
	binary.BigEndian.PutUint32(tamanhoErrado[0:], 4) // menor que o próprio cabeçalho
	naoComecaComCaixa := append(cx("abcd"), cx("moov")...)
	mvhdCurta := bytes.Join([][]byte{cx("ftyp"), cx("moov", cx("mvhd", []byte{0, 0, 0, 0}))}, nil)
	lixoNoFimDaLista := bytes.Join([][]byte{cx("ftyp"), cx("moov", cx("mvhd", cabecalhoComDatas(0)), []byte{1, 2, 3, 4})}, nil)
	for nome, b := range map[string][]byte{
		"HEIC (sem moov)": semMoov, "sobra no fim": sobra, "tamanho menor que o cabeçalho": tamanhoErrado,
		"não começa com caixa": naoComecaComCaixa, "mvhd curta": mvhdCurta, "lixo no fim da lista": lixoNoFimDaLista,
	} {
		if _, err := RemendosMP4(bytes.NewReader(b), int64(len(b))); !errors.Is(err, ErrFormato) {
			t.Errorf("%s: %v", nome, err)
		}
	}
}

// esparso é um arquivo grande que só tem bytes nos pontos dados; o resto é zero.
type esparso struct {
	tamanho int64
	pontos  map[int64][]byte
	falhaEm int64
}

func (e esparso) ReadAt(p []byte, off int64) (int, error) {
	if off == e.falhaEm {
		return 0, io.ErrUnexpectedEOF
	}
	clear(p)
	for pos, b := range e.pontos {
		if pos >= off && pos < off+int64(len(p)) {
			copy(p[pos-off:], b)
		}
	}
	return len(p), nil
}

func TestMP4CaixaDeMetadadoEnormeNaoEApagada(t *testing.T) {
	tam := int64(maxCaixaApagada + 1000)
	cab := func(n int64, tipo string) []byte {
		return append(binary.BigEndian.AppendUint32(nil, uint32(n)), tipo...)
	}
	e := esparso{tamanho: tam, falhaEm: -1, pontos: map[int64][]byte{
		0:  cab(16, "ftyp"),
		16: cab(tam-16, "moov"),
		24: cab(tam-24, "udta"),
	}}
	if _, err := RemendosMP4(e, tam); !errors.Is(err, ErrFormato) {
		t.Errorf("caixa enorme: %v", err)
	}
	// Erro de leitura no meio vira ErrFormato.
	for _, pos := range []int64{0, 24} {
		e.falhaEm = pos
		if _, err := RemendosMP4(e, tam); !errors.Is(err, ErrFormato) {
			t.Errorf("falha de leitura em %d: %v", pos, err)
		}
	}
}

func TestMP4LeituraFalhaEmCaixa64EEmDatas(t *testing.T) {
	cab := func(n uint32, tipo string) []byte { return append(binary.BigEndian.AppendUint32(nil, n), tipo...) }
	e := esparso{tamanho: 100, falhaEm: 8, pontos: map[int64][]byte{0: cab(1, "moov")}}
	if _, err := RemendosMP4(e, 100); !errors.Is(err, ErrFormato) {
		t.Errorf("tamanho de 64 bits ilegível: %v", err)
	}
	e = esparso{tamanho: 100, falhaEm: 16, pontos: map[int64][]byte{0: cab(100, "moov"), 8: cab(92, "mvhd")}}
	if _, err := RemendosMP4(e, 100); !errors.Is(err, ErrFormato) {
		t.Errorf("versão ilegível: %v", err)
	}
	if zeros(e, 16, 4) {
		t.Error("zeros com erro de leitura")
	}
}
