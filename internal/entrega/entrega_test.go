package entrega

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"disque-denuncia/internal/db"
	"disque-denuncia/internal/store"
)

// 09:00 de 28/10 em Brasília.
var agora = time.Date(2026, 10, 28, 12, 0, 0, 0, time.UTC)

const texto = "Uma descrição com mais de vinte caracteres."

// osFalso guarda o que chegou e pode recusar o n-ésimo request.
type osFalso struct {
	mu        sync.Mutex
	pedidos   []string
	denuncias []denunciaJSON
	imagens   map[string]string
	recusar   int // 1 = recusa o primeiro request; 0 = aceita tudo
	auth      []string
}

func (o *osFalso) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	o.mu.Lock()
	defer o.mu.Unlock()
	o.pedidos = append(o.pedidos, r.Method+" "+r.URL.Path)
	o.auth = append(o.auth, r.Header.Get("Authorization"))
	if o.recusar == len(o.pedidos) {
		http.Error(w, "fora do ar", http.StatusServiceUnavailable)
		return
	}
	corpo, _ := io.ReadAll(r.Body)
	if r.Method == "POST" {
		var d denunciaJSON
		json.Unmarshal(corpo, &d)
		o.denuncias = append(o.denuncias, d)
	} else {
		o.imagens[r.URL.Path] = r.Header.Get("Content-Type") + ":" + string(corpo)
	}
	w.WriteHeader(http.StatusOK)
}

func montar(t *testing.T) (*store.Store, *osFalso, *Entregador) {
	t.Helper()
	conn, err := db.Open(filepath.Join(t.TempDir(), "t.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { conn.Close() })
	st := store.New(conn)
	o := &osFalso{imagens: map[string]string{}}
	srv := httptest.NewServer(o)
	t.Cleanup(srv.Close)
	e := &Entregador{Fila: st, URL: srv.URL + "/", Token: "segredo", HoraEntrega: 8, Client: srv.Client(), Agora: func() time.Time { return agora }}
	return st, o, e
}

func TestEntregaDiasCompletosEApaga(t *testing.T) {
	st, o, e := montar(t)
	st.Registrar(store.Nova{Categoria: "fraude", Descricao: texto, Imagens: [][]byte{[]byte("jpg0"), []byte("jpg1")}}, agora.Add(-24*time.Hour))
	st.Registrar(store.Nova{Categoria: "outro", Descricao: texto}, agora) // hoje: fica para amanhã

	n, err := e.Tentar(context.Background())
	if err != nil || n != 1 {
		t.Fatalf("Tentar: %d %v", n, err)
	}
	if len(o.denuncias) != 1 {
		t.Fatalf("chegaram %d denúncias", len(o.denuncias))
	}
	d := o.denuncias[0]
	if d.Categoria != "fraude" || d.Descricao != texto || d.RecebidaEm != "2026-10-27" || d.TotalImagens != 2 || len(d.ID) != 36 {
		t.Errorf("payload: %+v", d)
	}
	for i, conteudo := range []string{"jpg0", "jpg1"} {
		caminho := "/api/denuncias/receber/" + d.ID + "/imagens/" + string(rune('0'+i))
		if o.imagens[caminho] != "image/jpeg:"+conteudo {
			t.Errorf("imagem %d: %q", i, o.imagens[caminho])
		}
	}
	for _, a := range o.auth {
		if a != "Bearer segredo" {
			t.Errorf("Authorization: %q", a)
		}
	}
	rest, _ := st.Listar("")
	if len(rest) != 1 || rest[0].Categoria != "outro" {
		t.Errorf("no SQLite ficou: %+v", rest)
	}
}

// O JSON entregue só tem os campos da denúncia: nada de quem enviou.
func TestPayloadSoComCamposDaDenuncia(t *testing.T) {
	b, _ := json.Marshal(denunciaJSON{})
	var campos map[string]any
	json.Unmarshal(b, &campos)
	if len(campos) != 5 {
		t.Errorf("campos: %v", campos)
	}
}

func TestAntesDaHoraNaoEntrega(t *testing.T) {
	st, o, e := montar(t)
	st.Registrar(store.Nova{Categoria: "outro", Descricao: texto}, agora.Add(-24*time.Hour))
	e.Agora = func() time.Time { return time.Date(2026, 10, 28, 10, 59, 0, 0, time.UTC) } // 07:59 BRT
	if n, err := e.Tentar(context.Background()); n != 0 || err != nil || len(o.pedidos) != 0 {
		t.Errorf("entregou antes da hora: %d %v %v", n, err, o.pedidos)
	}
}

func TestFalhaMantemNoSQLiteERepete(t *testing.T) {
	for _, recusar := range []int{1, 2} { // 1: recusa o POST; 2: recusa a imagem
		st, o, e := montar(t)
		st.Registrar(store.Nova{Categoria: "outro", Descricao: texto, Imagens: [][]byte{[]byte("x")}}, agora.Add(-24*time.Hour))
		o.recusar = recusar

		n, err := e.Tentar(context.Background())
		if n != 0 || err == nil || !strings.Contains(err.Error(), "HTTP 503") || !strings.Contains(err.Error(), "fora do ar") {
			t.Fatalf("recusar %d: %d %v", recusar, n, err)
		}
		if rest, _ := st.Listar(""); len(rest) != 1 {
			t.Fatalf("apagou sem confirmação (recusar %d)", recusar)
		}

		o.recusar = 0
		if n, err := e.Tentar(context.Background()); n != 1 || err != nil {
			t.Fatalf("segunda tentativa: %d %v", n, err)
		}
		if rest, _ := st.Listar(""); len(rest) != 0 {
			t.Error("não apagou depois de entregar")
		}
	}
}

func TestErroDeRede(t *testing.T) {
	st, _, e := montar(t)
	st.Registrar(store.Nova{Categoria: "outro", Descricao: texto}, agora.Add(-24*time.Hour))
	e.URL = "http://127.0.0.1:1"
	if _, err := e.Tentar(context.Background()); err == nil {
		t.Error("esperava erro de rede")
	}
	e.URL = "://quebrada"
	if _, err := e.Tentar(context.Background()); err == nil {
		t.Error("esperava erro de URL")
	}
}

type filaQuebrada struct {
	pendentes, anexo, remover error
}

func (f filaQuebrada) Pendentes(string) ([]store.Denuncia, error) {
	return []store.Denuncia{{ID: "x", Anexos: []string{"a"}}}, f.pendentes
}
func (f filaQuebrada) Anexo(string) ([]byte, error) { return []byte("j"), f.anexo }
func (f filaQuebrada) Remover(string) error         { return f.remover }

func TestErrosDaFila(t *testing.T) {
	_, _, e := montar(t)
	falha := errors.New("disco")
	for nome, f := range map[string]filaQuebrada{
		"pendentes": {pendentes: falha},
		"anexo":     {anexo: falha},
		"remover":   {remover: falha},
	} {
		e.Fila = f
		if _, err := e.Tentar(context.Background()); !errors.Is(err, falha) {
			t.Errorf("%s: %v", nome, err)
		}
	}
}

func TestRunParaComOContexto(t *testing.T) {
	st, o, e := montar(t)
	st.Registrar(store.Nova{Categoria: "outro", Descricao: texto}, agora.Add(-24*time.Hour))
	ctx, cancel := context.WithCancel(context.Background())
	feito := make(chan struct{})
	go func() { e.Run(ctx, time.Millisecond); close(feito) }()
	for range 200 {
		o.mu.Lock()
		n := len(o.denuncias)
		o.mu.Unlock()
		if n == 1 {
			break
		}
		time.Sleep(5 * time.Millisecond)
	}
	cancel()
	<-feito
	if len(o.denuncias) != 1 {
		t.Errorf("Run entregou %d", len(o.denuncias))
	}

	// Com o OS fora do ar, Run só registra e segue.
	_, o2, e2 := montar(t)
	e2.Fila = filaQuebrada{pendentes: errors.New("disco")}
	ctx2, cancel2 := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel2()
	e2.Run(ctx2, time.Millisecond)
	if len(o2.pedidos) != 0 {
		t.Error("não devia ter enviado")
	}
}

func TestUUID(t *testing.T) {
	if u := uuid("0123456789abcdef0123456789abcdef"); u != "01234567-89ab-cdef-0123-456789abcdef" {
		t.Error(u)
	}
	if uuid("curto") != "curto" {
		t.Error("curto")
	}
}
