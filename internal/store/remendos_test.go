package store

import (
	"bytes"
	"errors"
	"io"
	"testing"
)

// arquivoNoRascunho grava um arquivo de n bytes (0, 1, 2, … 250, 0, 1, …) num rascunho.
func arquivoNoRascunho(t *testing.T, s *Store, n int) (string, Arquivo, []byte) {
	t.Helper()
	dados := make([]byte, n)
	for i := range dados {
		dados[i] = byte(i % 251)
	}
	envio, err := s.Rascunho([]ArquivoPronto{{Dados: dados, Extensao: "mp4"}}, agora)
	if err != nil {
		t.Fatal(err)
	}
	arquivos, _ := s.ArquivosDoEnvio(envio)
	return envio, arquivos[0], dados
}

func TestLeitorAtravessaPartes(t *testing.T) {
	s := newStore(t)
	_, a, dados := arquivoNoRascunho(t, s, 2*TamParte+100)
	r := s.Leitor(a)
	for _, c := range []struct{ off, n int }{{0, 16}, {TamParte - 5, 10}, {TamParte*2 - 3, 50}, {len(dados) - 8, 8}} {
		p := make([]byte, c.n)
		if n, err := r.ReadAt(p, int64(c.off)); err != nil || n != c.n || !bytes.Equal(p, dados[c.off:c.off+c.n]) {
			t.Errorf("trecho %d+%d: %d %v", c.off, c.n, n, err)
		}
	}
	p := make([]byte, 20)
	if n, err := r.ReadAt(p, int64(len(dados)-8)); n != 8 || err != io.EOF {
		t.Errorf("passando do fim: %d %v", n, err)
	}
	if n, err := r.ReadAt(p, int64(len(dados)+5)); n != 0 || err != io.EOF {
		t.Errorf("depois do fim: %d %v", n, err)
	}
}

func TestLeitorParteQueFalta(t *testing.T) {
	s := newStore(t)
	_, a, _ := arquivoNoRascunho(t, s, TamParte+10)
	s.db.Exec(`DELETE FROM partes WHERE arquivo_id = ? AND n = 1`, a.ID)
	if _, err := s.Leitor(a).ReadAt(make([]byte, 20), TamParte-5); !errors.Is(err, io.ErrUnexpectedEOF) {
		t.Errorf("parte que falta: %v", err)
	}
	s.db.Close()
	if _, err := s.Leitor(a).ReadAt(make([]byte, 4), 0); err == nil {
		t.Error("banco fechado devia dar erro")
	}
}

func TestRemendarAtravessaPartesEMarcaLimpo(t *testing.T) {
	s := newStore(t)
	envio, a, dados := arquivoNoRascunho(t, s, 2*TamParte+100)
	if a.Limpo {
		t.Fatal("nasceu limpo")
	}
	remendo := bytes.Repeat([]byte{0xEE}, 20)
	err := s.Remendar(a.ID, []Remendo{{Pos: TamParte - 10, Dados: remendo}, {Pos: 4, Dados: []byte("free")}})
	if err != nil {
		t.Fatal(err)
	}
	copy(dados[TamParte-10:], remendo)
	copy(dados[4:], "free")
	var b bytes.Buffer
	s.LerArquivo(a.ID, &b)
	if !bytes.Equal(b.Bytes(), dados) {
		t.Error("o conteúdo remendado não bate")
	}
	arquivos, _ := s.ArquivosDoEnvio(envio)
	if !arquivos[0].Limpo || arquivos[0].Tamanho != a.Tamanho {
		t.Errorf("depois: %+v", arquivos[0])
	}
}

func TestRemendarRecusa(t *testing.T) {
	s := newStore(t)
	_, a, _ := arquivoNoRascunho(t, s, 100)
	if err := s.Remendar(a.ID, []Remendo{{Pos: 95, Dados: make([]byte, 10)}}); !errors.Is(err, ErrInvalid) {
		t.Errorf("passa do fim: %v", err)
	}
	if err := s.Remendar(a.ID, []Remendo{{Pos: -1, Dados: []byte{1}}}); !errors.Is(err, ErrInvalid) {
		t.Errorf("antes do começo: %v", err)
	}
	if err := s.Remendar("nao-existe", nil); !errors.Is(err, ErrNotFound) {
		t.Errorf("inexistente: %v", err)
	}
	s.db.Exec(`DELETE FROM partes WHERE arquivo_id = ?`, a.ID)
	if err := s.Remendar(a.ID, []Remendo{{Pos: 0, Dados: []byte{1}}}); err == nil {
		t.Error("sem a parte devia dar erro")
	}
	if got, _ := s.Arquivo(a.ID); got.Limpo {
		t.Error("marcou limpo mesmo com erro")
	}
	s.db.Close()
	if err := s.Remendar(a.ID, nil); err == nil {
		t.Error("banco fechado devia dar erro")
	}
}

func TestRascunhoEDescartar(t *testing.T) {
	s := newStore(t)
	envio, a, _ := arquivoNoRascunho(t, s, 10)
	if err := s.Descartar(envio); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Arquivo(a.ID); !errors.Is(err, ErrNotFound) {
		t.Error("o arquivo do rascunho descartado ficou")
	}
	if _, err := s.Rascunho([]ArquivoPronto{{Dados: nil}}, agora); !errors.Is(err, ErrInvalid) {
		t.Errorf("anexo vazio: %v", err)
	}
	var sobrou int
	s.db.QueryRow(`SELECT COUNT(*) FROM envios`).Scan(&sobrou)
	if sobrou != 0 {
		t.Error("o rascunho com erro ficou")
	}
}
