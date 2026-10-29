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

var nova = Nova{Categoria: "fraude", Descricao: texto}

func TestDiaEHoraEmBrasilia(t *testing.T) {
	if d := Dia(agora); d != "2026-10-27" {
		t.Errorf("Dia = %s", d)
	}
	if h := Hora(agora); h != 23 {
		t.Errorf("Hora = %d", h)
	}
}

func TestPartes(t *testing.T) {
	for _, tc := range []struct {
		tamanho int64
		partes  int
		ultima  int64
	}{{1, 1, 1}, {TamParte, 1, TamParte}, {TamParte + 1, 2, 1}, {3 * TamParte, 3, TamParte}} {
		if p := Partes(tc.tamanho); p != tc.partes {
			t.Errorf("Partes(%d) = %d", tc.tamanho, p)
		}
		if u := tamanhoDaParte(tc.tamanho, tc.partes-1); u != tc.ultima {
			t.Errorf("última parte de %d = %d", tc.tamanho, u)
		}
	}
}

func TestLimparExtensao(t *testing.T) {
	for in, want := range map[string]string{"PDF": "pdf", ".jpg": "jpg", " mp4 ": "mp4", "tar.gz": "", "a/b": "", "": "", "abcdefghijk": ""} {
		if got := LimparExtensao(in); got != want {
			t.Errorf("%q → %q, esperava %q", in, got, want)
		}
	}
}

// Fluxo do navegador: rascunho, um arquivo de 2 partes, concluir.
func TestEnvioEmPartes(t *testing.T) {
	s := newStore(t)
	envio, err := s.NovoEnvio(agora)
	if err != nil || len(envio) != 32 {
		t.Fatalf("NovoEnvio: %q %v", envio, err)
	}
	conteudo := append([]byte("%PDF-1.4 "), bytes.Repeat([]byte("x"), TamParte)...)
	arq, err := s.NovoArquivo(envio, int64(len(conteudo)), ".PDF")
	if err != nil {
		t.Fatal(err)
	}
	// A parte 1 chega antes da 0, e a 0 é reenviada: a ordem e a repetição não importam.
	for _, k := range []int{1, 0, 0} {
		if err := s.GravarParte(envio, arq, k, conteudo[k*TamParte:min(len(conteudo), (k+1)*TamParte)]); err != nil {
			t.Fatalf("parte %d: %v", k, err)
		}
	}
	if err := s.Concluir(envio, nova, agora); err != nil {
		t.Fatal(err)
	}

	list, _ := s.Listar("")
	if len(list) != 1 || len(list[0].Anexos) != 1 {
		t.Fatalf("listar: %+v", list)
	}
	d, err := s.Buscar(list[0].ID)
	if err != nil || d.RecebidaEm != "2026-10-27" || d.Descricao != texto {
		t.Fatalf("buscar: %+v %v", d, err)
	}
	a := d.Anexos[0]
	if a.Extensao != "pdf" || a.Tipo != "application/pdf" || a.Tamanho != int64(len(conteudo)) {
		t.Errorf("anexo: %+v", a)
	}
	var lido bytes.Buffer
	if err := s.LerArquivo(a.ID, &lido); err != nil || !bytes.Equal(lido.Bytes(), conteudo) {
		t.Errorf("conteúdo remontado errado (%d bytes, %v)", lido.Len(), err)
	}
	if ds, _ := s.ArquivosDoEnvio(envio); len(ds) != 0 {
		t.Error("o rascunho ficou com arquivos")
	}
	if _, err := s.NovoArquivo(envio, 1, "txt"); !errors.Is(err, ErrNotFound) {
		t.Errorf("o rascunho devia ter sumido: %v", err)
	}
}

func TestConcluirRecusaArquivoIncompleto(t *testing.T) {
	s := newStore(t)
	envio, _ := s.NovoEnvio(agora)
	arq, _ := s.NovoArquivo(envio, TamParte+10, "bin")
	s.GravarParte(envio, arq, 0, make([]byte, TamParte))
	err := s.Concluir(envio, nova, agora)
	if !errors.Is(err, ErrInvalid) || !strings.Contains(err.Error(), "arquivo 1") {
		t.Fatalf("esperava arquivo incompleto, veio %v", err)
	}
	if list, _ := s.Listar(""); len(list) != 0 {
		t.Error("gravou a denúncia com arquivo incompleto")
	}
}

func TestNovoArquivoLimites(t *testing.T) {
	s := newStore(t)
	envio, _ := s.NovoEnvio(agora)
	if _, err := s.NovoArquivo(envio, 0, "x"); !errors.Is(err, ErrInvalid) {
		t.Errorf("vazio: %v", err)
	}
	if _, err := s.NovoArquivo("nao-existe", 1, "x"); !errors.Is(err, ErrNotFound) {
		t.Errorf("rascunho inexistente: %v", err)
	}
	if _, err := s.NovoArquivo(envio, MaxTotal-10, "x"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.NovoArquivo(envio, 11, "x"); !errors.Is(err, ErrInvalid) {
		t.Errorf("passou de 5 GB no total: %v", err)
	} else if !strings.Contains(err.Error(), "5 GB") {
		t.Errorf("mensagem: %v", err)
	}
	envio2, _ := s.NovoEnvio(agora)
	for range MaxArquivos {
		if _, err := s.NovoArquivo(envio2, 1, "x"); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := s.NovoArquivo(envio2, 1, "x"); !errors.Is(err, ErrInvalid) {
		t.Errorf("arquivos demais: %v", err)
	}
}

func TestGravarParteValida(t *testing.T) {
	s := newStore(t)
	envio, _ := s.NovoEnvio(agora)
	outro, _ := s.NovoEnvio(agora)
	arq, _ := s.NovoArquivo(envio, 10, "x")
	if err := s.GravarParte(outro, arq, 0, make([]byte, 10)); !errors.Is(err, ErrNotFound) {
		t.Errorf("arquivo de outro rascunho: %v", err)
	}
	if err := s.GravarParte(envio, arq, 1, make([]byte, 10)); !errors.Is(err, ErrInvalid) {
		t.Errorf("parte fora: %v", err)
	}
	if err := s.GravarParte(envio, arq, -1, make([]byte, 10)); !errors.Is(err, ErrInvalid) {
		t.Errorf("parte negativa: %v", err)
	}
	if err := s.GravarParte(envio, arq, 0, make([]byte, 9)); !errors.Is(err, ErrInvalid) {
		t.Errorf("tamanho errado: %v", err)
	}
}

func TestLimiteDeRascunhosPorDia(t *testing.T) {
	s := newStore(t)
	for range LimiteEnviosPorDia {
		if _, err := s.NovoEnvio(agora); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := s.NovoEnvio(agora); !errors.Is(err, ErrLimite) {
		t.Errorf("esperava ErrLimite, veio %v", err)
	}
}

func TestLimparRascunhos(t *testing.T) {
	s := newStore(t)
	velho, _ := s.NovoEnvio(agora.Add(-48 * time.Hour))
	arq, _ := s.NovoArquivo(velho, 3, "x")
	s.GravarParte(velho, arq, 0, []byte("abc"))
	ontem, _ := s.NovoEnvio(agora.Add(-24 * time.Hour))
	if n, err := s.LimparRascunhos(agora); n != 1 || err != nil {
		t.Fatalf("apagou %d (%v)", n, err)
	}
	if restantes, err := s.ArquivosDoEnvio(velho); err != nil || len(restantes) != 0 {
		t.Errorf("o arquivo do rascunho abandonado ficou: %v %v", restantes, err)
	}
	if _, err := s.NovoArquivo(ontem, 1, "x"); err != nil {
		t.Errorf("o rascunho de ontem não podia sumir: %v", err)
	}
}

func TestSubstituirArquivo(t *testing.T) {
	s := newStore(t)
	envio, _ := s.NovoEnvio(agora)
	pos, _ := s.NovoArquivo(envio, 3, "png")
	s.GravarParte(envio, pos, 0, []byte("abc"))
	arquivos, _ := s.ArquivosDoEnvio(envio)
	arq := arquivos[0].ID
	limpo := bytes.Repeat([]byte("j"), TamParte+5)
	if err := s.SubstituirArquivo(Arquivo{ID: arq, Tipo: "image/jpeg", Extensao: "jpg"}, limpo); err != nil {
		t.Fatal(err)
	}
	a, _ := s.Arquivo(arq)
	var lido bytes.Buffer
	s.LerArquivo(arq, &lido)
	if a.Tamanho != int64(len(limpo)) || a.Tipo != "image/jpeg" || a.Extensao != "jpg" || !bytes.Equal(lido.Bytes(), limpo) {
		t.Errorf("%+v, %d bytes", a, lido.Len())
	}
	if err := s.SubstituirArquivo(Arquivo{ID: arq}, nil); !errors.Is(err, ErrInvalid) {
		t.Errorf("vazio: %v", err)
	}
	if err := s.Concluir(envio, nova, agora); err != nil {
		t.Errorf("o arquivo substituído devia estar completo: %v", err)
	}
}

func TestRegistrarDeUmaVez(t *testing.T) {
	s := newStore(t)
	err := s.Registrar(nova, []ArquivoPronto{{Dados: []byte("um"), Extensao: "txt"}, {Dados: []byte("dois"), Extensao: "txt"}}, agora)
	if err != nil {
		t.Fatal(err)
	}
	list, _ := s.Listar("")
	if len(list) != 1 || len(list[0].Anexos) != 2 {
		t.Fatalf("%+v", list)
	}
	var rascunhos int
	s.db.QueryRow(`SELECT COUNT(*) FROM envios`).Scan(&rascunhos)
	if rascunhos != 0 {
		t.Error("sobrou rascunho")
	}
}

func TestRegistrarFalhaNaoDeixaRascunho(t *testing.T) {
	s := newStore(t)
	if err := s.Registrar(Nova{Categoria: "x", Descricao: texto}, nil, agora); !errors.Is(err, ErrInvalid) {
		t.Errorf("categoria: %v", err)
	}
	if err := s.Registrar(nova, []ArquivoPronto{{Dados: nil}}, agora); !errors.Is(err, ErrInvalid) {
		t.Errorf("anexo vazio: %v", err)
	}
	for range LimitePorDia {
		s.Registrar(nova, nil, agora)
	}
	if err := s.Registrar(nova, nil, agora); !errors.Is(err, ErrLimite) {
		t.Errorf("limite: %v", err)
	}
	var rascunhos int
	s.db.QueryRow(`SELECT COUNT(*) FROM envios`).Scan(&rascunhos)
	if rascunhos != 0 {
		t.Errorf("sobraram %d rascunhos", rascunhos)
	}
	// O dia seguinte em Brasília abre o canal de novo.
	if err := s.Registrar(nova, nil, agora.Add(time.Hour)); err != nil {
		t.Errorf("no dia seguinte: %v", err)
	}
}

func TestValidar(t *testing.T) {
	for nome, n := range map[string]Nova{
		"categoria inválida": {Categoria: "qualquer", Descricao: texto},
		"descrição curta":    {Categoria: "outro", Descricao: "curta demais"},
		"só espaços":         {Categoria: "outro", Descricao: strings.Repeat(" ", 50)},
		"descrição longa":    {Categoria: "outro", Descricao: strings.Repeat("x", DescricaoMax+1)},
	} {
		if err := n.Validar(); !errors.Is(err, ErrInvalid) {
			t.Errorf("%s: %v", nome, err)
		}
	}
	// Acentos contam como um caractere: 20 letras acentuadas passam.
	if err := (Nova{Categoria: "outro", Descricao: strings.Repeat("é", DescricaoMin)}).Validar(); err != nil {
		t.Error(err)
	}
}

func TestConcluirRascunhoInexistente(t *testing.T) {
	if err := newStore(t).Concluir("x", nova, agora); !errors.Is(err, ErrNotFound) {
		t.Error(err)
	}
}

func TestListarOrdemEFiltro(t *testing.T) {
	s := newStore(t)
	s.Registrar(Nova{Categoria: "outro", Descricao: texto}, nil, agora.Add(-48*time.Hour))
	s.Registrar(nova, []ArquivoPronto{{Dados: []byte{1}}}, agora)
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
	s.Registrar(nova, nil, agora)
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
	if _, err := s.Arquivo("x"); !errors.Is(err, ErrNotFound) {
		t.Error(err)
	}
	if err := s.GravarParte("x", 0, 0, []byte{1}); !errors.Is(err, ErrNotFound) {
		t.Error(err)
	}
}

func TestPendentesSoDiasCompletos(t *testing.T) {
	s := newStore(t)
	s.Registrar(Nova{Categoria: "outro", Descricao: texto}, []ArquivoPronto{{Dados: []byte("a")}, {Dados: []byte("b")}}, agora.Add(-24*time.Hour))
	s.Registrar(nova, nil, agora)

	list, err := s.Pendentes(Dia(agora))
	if err != nil {
		t.Fatal(err)
	}
	if len(list) != 1 || list[0].Categoria != "outro" || len(list[0].Anexos) != 2 {
		t.Fatalf("pendentes: %+v", list)
	}
	if err := s.Remover(list[0].ID); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Arquivo(list[0].Anexos[0].ID); !errors.Is(err, ErrNotFound) {
		t.Error("o anexo ficou depois de remover")
	}
	var partes int
	s.db.QueryRow(`SELECT COUNT(*) FROM partes`).Scan(&partes)
	if partes != 0 {
		t.Errorf("sobraram %d partes", partes)
	}
	if rest, _ := s.Pendentes(Dia(agora.Add(24 * time.Hour))); len(rest) != 1 || rest[0].Categoria != "fraude" {
		t.Errorf("amanhã: %+v", rest)
	}
}

// Com o banco fechado, todo método devolve o erro em vez de seguir em frente.
func TestErrosDoBanco(t *testing.T) {
	s := newStore(t)
	s.db.Close()
	checar := map[string]error{}
	_, checar["NovoEnvio"] = s.NovoEnvio(agora)
	_, checar["NovoArquivo"] = s.NovoArquivo("x", 1, "")
	checar["GravarParte"] = s.GravarParte("x", 0, 0, []byte{1})
	_, checar["ArquivosDoEnvio"] = s.ArquivosDoEnvio("x")
	checar["LerArquivo"] = s.LerArquivo("x", &bytes.Buffer{})
	checar["SubstituirArquivo"] = s.SubstituirArquivo(Arquivo{ID: "x"}, []byte{1})
	_, checar["LimparRascunhos"] = s.LimparRascunhos(agora)
	checar["Concluir"] = s.Concluir("x", nova, agora)
	_, checar["Listar"] = s.Listar("")
	_, checar["Buscar"] = s.Buscar("x")
	_, checar["Arquivo"] = s.Arquivo("x")
	checar["Atualizar"] = s.Atualizar("x", "nova", "", agora)
	_, checar["Pendentes"] = s.Pendentes("2026-10-28")
	checar["Remover"] = s.Remover("x")
	_, checar["Authenticate"] = s.Authenticate("a", "b")
	for nome, err := range checar {
		if err == nil || errors.Is(err, ErrNotFound) {
			t.Errorf("%s: %v", nome, err)
		}
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

// O teto do canal soma os arquivos de todos os rascunhos e denúncias guardados, e
// libera quando a entrega apaga o que entregou.
func TestEspacoDoCanal(t *testing.T) {
	s := newStore(t)
	s.EspacoMax = 100
	um, _ := s.NovoEnvio(agora)
	if _, err := s.NovoArquivo(um, 60, "x"); err != nil {
		t.Fatal(err)
	}
	dois, _ := s.NovoEnvio(agora)
	if _, err := s.NovoArquivo(dois, 41, "x"); !errors.Is(err, ErrCheio) {
		t.Fatalf("esperava canal cheio: %v", err)
	}
	if _, err := s.NovoArquivo(dois, 40, "x"); err != nil {
		t.Fatalf("cabia exatamente: %v", err)
	}
	s.db.Exec(`DELETE FROM envios WHERE id = ?`, um)
	if _, err := s.NovoArquivo(dois, 60, "x"); err != nil {
		t.Errorf("o espaço não voltou depois de apagar: %v", err)
	}
	if New(s.db).EspacoMax != EspacoPadrao {
		t.Error("New sem o teto padrão")
	}
}

func TestRotuloTamanho(t *testing.T) {
	for b, quer := range map[int64]string{MaxTotal: "5 GB", 512 << 20: "512 MB", 1 << 30: "1 GB", 1536 << 20: "1536 MB"} {
		if got := RotuloTamanho(b); got != quer {
			t.Errorf("%d → %s", b, got)
		}
	}
}
