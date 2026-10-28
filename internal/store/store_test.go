package store

import (
	"bytes"
	"errors"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"disque-denuncia/internal/db"
)

func newStore(t *testing.T) *Store {
	t.Helper()
	conn, err := db.Open(filepath.Join(t.TempDir(), "t.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { conn.Close() })
	return New(conn)
}

// 23:30 do dia 27 em Brasília, já dia 28 em UTC.
var agora = time.Date(2026, 10, 28, 2, 30, 0, 0, time.UTC)

const texto = "Uma descrição com mais de vinte caracteres."

func TestDiaEmBrasilia(t *testing.T) {
	if d := Dia(agora); d != "2026-10-27" {
		t.Errorf("Dia = %s", d)
	}
}

func TestRegistrarEBuscar(t *testing.T) {
	s := newStore(t)
	n := Nova{Categoria: "fraude", Descricao: "  " + texto + "  ", Imagens: [][]byte{[]byte("img0"), []byte("img1")}}
	if err := s.Registrar(n, agora); err != nil {
		t.Fatal(err)
	}
	list, err := s.Listar("")
	if err != nil || len(list) != 1 {
		t.Fatalf("listar: %v %v", list, err)
	}
	d, err := s.Buscar(list[0].ID)
	if err != nil {
		t.Fatal(err)
	}
	if d.Categoria != "fraude" || d.Descricao != texto || d.RecebidaEm != "2026-10-27" || d.Status != "nova" || d.AtualizadaEm != "" {
		t.Errorf("denúncia gravada: %+v", d)
	}
	if len(d.ID) != 32 || len(d.Anexos) != 2 {
		t.Fatalf("id %q, anexos %v", d.ID, d.Anexos)
	}
	for i, a := range d.Anexos {
		jpeg, err := s.Anexo(a)
		if err != nil || !bytes.Equal(jpeg, []byte("img"+string(rune('0'+i)))) {
			t.Errorf("anexo %d: %q %v", i, jpeg, err)
		}
	}
}

func TestRegistrarValida(t *testing.T) {
	s := newStore(t)
	for nome, n := range map[string]Nova{
		"categoria inválida": {Categoria: "qualquer", Descricao: texto},
		"descrição curta":    {Categoria: "outro", Descricao: "curta demais"},
		"só espaços":         {Categoria: "outro", Descricao: strings.Repeat(" ", 50)},
		"descrição longa":    {Categoria: "outro", Descricao: strings.Repeat("x", DescricaoMax+1)},
		"imagens demais":     {Categoria: "outro", Descricao: texto, Imagens: make([][]byte, MaxImagens+1)},
	} {
		if err := s.Registrar(n, agora); !errors.Is(err, ErrInvalid) {
			t.Errorf("%s: %v", nome, err)
		}
	}
	if list, _ := s.Listar(""); len(list) != 0 {
		t.Errorf("gravou %d inválidas", len(list))
	}
}

// Acentos contam como um caractere: 20 letras acentuadas passam.
func TestDescricaoContaCaracteresNaoBytes(t *testing.T) {
	n := Nova{Categoria: "outro", Descricao: strings.Repeat("é", DescricaoMin)}
	if err := n.Validar(); err != nil {
		t.Error(err)
	}
}

func TestLimitePorDia(t *testing.T) {
	s := newStore(t)
	for range LimitePorDia {
		if err := s.Registrar(Nova{Categoria: "outro", Descricao: texto}, agora); err != nil {
			t.Fatal(err)
		}
	}
	if err := s.Registrar(Nova{Categoria: "outro", Descricao: texto}, agora); !errors.Is(err, ErrLimite) {
		t.Fatalf("esperava ErrLimite, veio %v", err)
	}
	// O dia seguinte em Brasília abre o canal de novo.
	if err := s.Registrar(Nova{Categoria: "outro", Descricao: texto}, agora.Add(time.Hour)); err != nil {
		t.Errorf("no dia seguinte: %v", err)
	}
}

func TestListarOrdemEFiltro(t *testing.T) {
	s := newStore(t)
	s.Registrar(Nova{Categoria: "outro", Descricao: texto}, agora.Add(-48*time.Hour))
	s.Registrar(Nova{Categoria: "fraude", Descricao: texto, Imagens: [][]byte{{1}}}, agora)
	list, err := s.Listar("")
	if err != nil || len(list) != 2 {
		t.Fatalf("%v %v", list, err)
	}
	if list[0].RecebidaEm != "2026-10-27" || list[1].RecebidaEm != "2026-10-25" || len(list[0].Anexos) != 1 {
		t.Errorf("ordem/anexos: %+v", list)
	}

	if err := s.Atualizar(list[1].ID, "concluida", "", agora); err != nil {
		t.Fatal(err)
	}
	so, _ := s.Listar("concluida")
	if len(so) != 1 || so[0].ID != list[1].ID {
		t.Errorf("filtro: %+v", so)
	}
}

func TestAtualizar(t *testing.T) {
	s := newStore(t)
	s.Registrar(Nova{Categoria: "outro", Descricao: texto}, agora)
	list, _ := s.Listar("")
	id := list[0].ID

	if err := s.Atualizar(id, "em_analise", "  ouvimos as partes  ", agora); err != nil {
		t.Fatal(err)
	}
	d, _ := s.Buscar(id)
	if d.Status != "em_analise" || d.Parecer != "ouvimos as partes" || d.AtualizadaEm != "2026-10-27 23:30" {
		t.Errorf("%+v", d)
	}
	if err := s.Atualizar(id, "apagada", "", agora); !errors.Is(err, ErrInvalid) {
		t.Errorf("status inválido: %v", err)
	}
	if err := s.Atualizar(id, "nova", strings.Repeat("x", ParecerMax+1), agora); !errors.Is(err, ErrInvalid) {
		t.Errorf("parecer longo: %v", err)
	}
	if err := s.Atualizar("nao-existe", "nova", "", agora); !errors.Is(err, ErrNotFound) {
		t.Errorf("inexistente: %v", err)
	}
}

func TestNaoEncontrado(t *testing.T) {
	s := newStore(t)
	if _, err := s.Buscar("x"); !errors.Is(err, ErrNotFound) {
		t.Error(err)
	}
	if _, err := s.Anexo("x"); !errors.Is(err, ErrNotFound) {
		t.Error(err)
	}
}

// Com o banco fechado, todo método devolve o erro em vez de seguir em frente.
func TestErrosDoBanco(t *testing.T) {
	s := newStore(t)
	s.db.Close()
	if err := s.Registrar(Nova{Categoria: "outro", Descricao: texto}, agora); err == nil {
		t.Error("Registrar")
	}
	if _, err := s.Listar(""); err == nil {
		t.Error("Listar")
	}
	if _, err := s.Buscar("x"); err == nil || errors.Is(err, ErrNotFound) {
		t.Error("Buscar")
	}
	if err := s.Atualizar("x", "nova", "", agora); err == nil || errors.Is(err, ErrNotFound) {
		t.Error("Atualizar")
	}
	if _, err := s.Authenticate("a", "b"); err == nil {
		t.Error("Authenticate")
	}
}

func TestRotulo(t *testing.T) {
	if Rotulo(Categorias, "fraude") != "Fraude, desvio ou corrupção" || Rotulo(Status, "x") != "x" {
		t.Error("rótulo")
	}
}

func TestAuthenticate(t *testing.T) {
	s := newStore(t)
	// admin/segredo, hash do htpasswd ($2y$).
	_, err := s.db.Exec(`INSERT INTO users (username, password_hash) VALUES ('admin', '$2y$04$iOd4Uz.siLSFfamqbwlv3eM7ZRsm4H4gdq3h8Oigbsmcnw3kyoZvu')`)
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		user, pass string
		want       bool
	}{
		{"admin", "segredo", true},
		{"admin", "errada", false},
		{"outro", "segredo", false},
		{"", "", false},
	} {
		ok, err := s.Authenticate(tc.user, tc.pass)
		if err != nil || ok != tc.want {
			t.Errorf("%s/%s: %v %v, esperava %v", tc.user, tc.pass, ok, err, tc.want)
		}
	}
}
