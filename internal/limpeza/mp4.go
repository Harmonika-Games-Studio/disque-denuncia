package limpeza

import (
	"encoding/binary"
	"fmt"
	"io"
)

// Vídeo e áudio no formato de caixas (ISO BMFF / QuickTime): MP4, MOV, M4A, 3GP.
//
// Onde mora o que identifica, num vídeo de celular:
//   - moov/udta (©xyz = GPS, ©mak/©mod = aparelho, ©day = data, ©too = programa);
//   - moov/meta (o iPhone grava ali com.apple.quicktime.location.ISO6709, make, model,
//     software, creationdate);
//   - caixas uuid (XMP de câmeras, perfis da Sony com GPS);
//   - as datas de criação em mvhd, tkhd e mdhd.
//
// As caixas de metadado viram "free" (caixa vazia que todo player pula) com o conteúdo
// zerado, e as datas viram zero (1904-01-01). Nada muda de tamanho nem de lugar, então
// os índices que apontam para o vídeo (stco/co64) continuam certos.
//
// Não sai: telemetria que vem como faixa própria dentro do vídeo (GPS de GoPro e de
// drone DJI), que fica em mdat junto com a imagem.

// caixaDeTopo: tipos que abrem um arquivo de caixas (o MOV antigo não tem ftyp).
func caixaDeTopo(tipo string) bool {
	switch tipo {
	case "ftyp", "moov", "mdat", "wide", "free", "skip":
		return true
	}
	return false
}

type caixa struct {
	tipo               string
	inicio, corpo, fim int64
}

// maxCaixaApagada: uma caixa de metadado maior que isso não é zerada (seria um
// "metadado" de centenas de MB; o arquivo provavelmente não é o que parece).
const maxCaixaApagada = 64 << 20

// RemendosMP4 devolve os remendos que tiram os metadados. ErrFormato se as caixas do
// topo não cobrem o arquivo exatamente ou se não há moov (um HEIC, por exemplo, tem
// caixas mas guarda o EXIF de outro jeito, e apagar o meta dele estragaria a foto).
func RemendosMP4(r io.ReaderAt, tamanho int64) ([]Remendo, error) {
	var out []Remendo
	temMoov := false
	err := lerCaixas(r, 0, tamanho, true, func(c caixa) error {
		switch c.tipo {
		case "moov":
			temMoov = true
			return remendarMoov(r, c, &out)
		case "udta", "meta", "uuid":
			return apagarCaixa(c, &out)
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	if !temMoov {
		return nil, fmt.Errorf("%w: arquivo de caixas sem moov", ErrFormato)
	}
	return out, nil
}

func remendarMoov(r io.ReaderAt, moov caixa, out *[]Remendo) error {
	return lerCaixas(r, moov.corpo, moov.fim, false, func(c caixa) error {
		switch c.tipo {
		case "mvhd":
			return zerarDatas(r, c, out)
		case "trak":
			return remendarTrak(r, c, out)
		case "udta", "meta", "uuid":
			return apagarCaixa(c, out)
		}
		return nil
	})
}

func remendarTrak(r io.ReaderAt, trak caixa, out *[]Remendo) error {
	return lerCaixas(r, trak.corpo, trak.fim, false, func(c caixa) error {
		switch c.tipo {
		case "tkhd":
			return zerarDatas(r, c, out)
		case "mdia":
			return lerCaixas(r, c.corpo, c.fim, false, func(m caixa) error {
				if m.tipo == "mdhd" {
					return zerarDatas(r, m, out)
				}
				return nil
			})
		case "udta", "meta", "uuid":
			return apagarCaixa(c, out)
		}
		return nil
	})
}

// lerCaixas percorre as caixas de [de, ate). No topo, elas têm que fechar exatamente
// no fim do arquivo; dentro de uma caixa, o QuickTime pode terminar a lista com 4
// bytes zero, que são aceitos.
func lerCaixas(r io.ReaderAt, de, ate int64, topo bool, f func(caixa) error) error {
	for pos := de; pos < ate; {
		resto := ate - pos
		if resto < 8 {
			if !topo && resto == 4 && zeros(r, pos, 4) {
				return nil
			}
			return fmt.Errorf("%w: sobra de %d bytes em %d", ErrFormato, resto, pos)
		}
		c, err := lerCabecalho(r, pos, ate)
		if err != nil {
			return err
		}
		if topo && pos == 0 && !caixaDeTopo(c.tipo) {
			return fmt.Errorf("%w: começa com %q", ErrFormato, c.tipo)
		}
		if err := f(c); err != nil {
			return err
		}
		pos = c.fim
	}
	return nil
}

func lerCabecalho(r io.ReaderAt, pos, ate int64) (caixa, error) {
	var h [16]byte
	if _, err := r.ReadAt(h[:8], pos); err != nil {
		return caixa{}, fmt.Errorf("%w: %w", ErrFormato, err)
	}
	c := caixa{tipo: string(h[4:8]), inicio: pos, corpo: pos + 8}
	tam := int64(binary.BigEndian.Uint32(h[:4]))
	switch tam {
	case 0: // vai até o fim de quem a contém
		tam = ate - pos
	case 1: // tamanho de 64 bits logo depois do tipo
		if _, err := r.ReadAt(h[8:16], pos+8); err != nil {
			return caixa{}, fmt.Errorf("%w: %w", ErrFormato, err)
		}
		tam = int64(binary.BigEndian.Uint64(h[8:16]))
		c.corpo += 8
	}
	if tam < c.corpo-pos || tam > ate-pos {
		return caixa{}, fmt.Errorf("%w: caixa %q com tamanho %d em %d", ErrFormato, c.tipo, tam, pos)
	}
	c.fim = pos + tam
	return c, nil
}

// apagarCaixa troca o tipo para "free" e zera o conteúdo (o usertype de uma uuid incluso).
func apagarCaixa(c caixa, out *[]Remendo) error {
	if c.fim-c.corpo > maxCaixaApagada {
		return fmt.Errorf("%w: caixa %q de %d bytes", ErrFormato, c.tipo, c.fim-c.corpo)
	}
	*out = append(*out,
		Remendo{Pos: c.inicio + 4, Dados: []byte("free")},
		Remendo{Pos: c.corpo, Dados: make([]byte, c.fim-c.corpo)},
	)
	return nil
}

// zerarDatas zera creation_time e modification_time de mvhd/tkhd/mdhd: 4 bytes cada na
// versão 0, 8 na versão 1, logo depois de versão (1 byte) e flags (3 bytes).
func zerarDatas(r io.ReaderAt, c caixa, out *[]Remendo) error {
	var versao [1]byte
	if _, err := r.ReadAt(versao[:], c.corpo); err != nil {
		return fmt.Errorf("%w: %w", ErrFormato, err)
	}
	largura := int64(4)
	if versao[0] == 1 {
		largura = 8
	}
	if c.corpo+4+2*largura > c.fim {
		return fmt.Errorf("%w: %q curta demais", ErrFormato, c.tipo)
	}
	*out = append(*out, Remendo{Pos: c.corpo + 4, Dados: make([]byte, 2*largura)})
	return nil
}

func zeros(r io.ReaderAt, pos int64, n int) bool {
	b := make([]byte, n)
	if _, err := r.ReadAt(b, pos); err != nil {
		return false
	}
	for _, x := range b {
		if x != 0 {
			return false
		}
	}
	return true
}
