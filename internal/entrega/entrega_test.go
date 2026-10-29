package entrega

import (
	"bytes"
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

var ontem = agora.Add(-24 * time.Hour)

// osFalso faz o papel do HarmonikaOS e do Storage: a rota de anexo devolve uma URL
// de upload que aponta para o próprio servidor de teste.
type osFalso struct {
	mu        sync.Mutex
	url       string
	pedidos   []string
	auth      map[string]string
	denuncias []denunciaJSON
	subidos   map[string][]byte
	tipos     map[string]string // Content-Type de cada PUT no Storage
	existe    map[int]bool      // anexos que o OS já tem
	recusar   string            // prefixo de "MÉTODO caminho" a recusar com 503
	resposta  string            // corpo forçado para a rota de anexo
}

func (o *osFalso) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	o.mu.Lock()
	defer o.mu.Unlock()
	pedido := r.Method + " " + r.URL.Path
	o.pedidos = append(o.pedidos, pedido)
	o.auth[pedido] = r.Header.Get("Authorization")
	if o.recusar != "" && strings.HasPrefix(pedido, o.recusar) {
		http.Error(w, "fora do ar", http.StatusServiceUnavailable)
		return
	}
	corpo, _ := io.ReadAll(r.Body)
	switch {
	case pedido == "POST /api/denuncias/receber":
		var d denunciaJSON
		json.Unmarshal(corpo, &d)
		o.denuncias = append(o.denuncias, d)
	case strings.HasPrefix(pedido, "POST /api/denuncias/receber/"):
		o.responderAnexo(w, r)
	case r.Method == "PUT" && strings.HasPrefix(r.URL.Path, "/storage/"):
		o.subidos[r.URL.Path] = corpo
		o.tipos[r.URL.Path] = r.Header.Get("Content-Type")
	}
}

func (o *osFalso) responderAnexo(w http.ResponseWriter, r *http.Request) {
	if o.resposta != "" {
		io.WriteString(w, o.resposta)
		return
	}
	partes := strings.Split(r.URL.Path, "/")
	n := partes[len(partes)-1]
	if o.existe[int(n[0]-'0')] {
		io.WriteString(w, `{"existe":true}`)
		return
	}
	json.NewEncoder(w).Encode(destinoJSON{URL: o.url + "/storage/" + partes[4] + "/" + n + "?token=assinado"})
}

func montar(t *testing.T) (*store.Store, *osFalso, *Entregador) {
	t.Helper()
	conn, err := db.Open(filepath.Join(t.TempDir(), "t.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { conn.Close() })
	st := store.New(conn)
	o := &osFalso{auth: map[string]string{}, subidos: map[string][]byte{}, tipos: map[string]string{}, existe: map[int]bool{}}
	srv := httptest.NewServer(o)
	t.Cleanup(srv.Close)
	o.url = srv.URL
	e := &Entregador{Fila: st, URL: srv.URL + "/", Token: "segredo", HoraEntrega: 8, Client: srv.Client(), Agora: func() time.Time { return agora }}
	return st, o, e
}

func registrar(t *testing.T, st *store.Store, quando time.Time, anexos ...store.ArquivoPronto) {
	t.Helper()
	if err := st.Registrar(store.Nova{Categoria: "fraude", Descricao: texto}, anexos, quando); err != nil {
		t.Fatal(err)
	}
}

func TestEntregaDiasCompletosEApaga(t *testing.T) {
	st, o, e := montar(t)
	grande := bytes.Repeat([]byte("v"), store.TamParte+100) // duas partes no SQLite
	registrar(t, st, ontem, store.ArquivoPronto{Dados: []byte("%PDF-1.4 x"), Extensao: "pdf"}, store.ArquivoPronto{Dados: grande, Extensao: "mp4"})
	registrar(t, st, agora) // hoje: fica para amanhã

	n, err := e.Tentar(context.Background())
	if err != nil || n != 1 {
		t.Fatalf("Tentar: %d %v", n, err)
	}
	if len(o.denuncias) != 1 {
		t.Fatalf("chegaram %d denúncias", len(o.denuncias))
	}
	d := o.denuncias[0]
	if d.Categoria != "fraude" || d.Descricao != texto || d.RecebidaEm != "2026-10-27" || len(d.ID) != 36 {
		t.Errorf("payload: %+v", d)
	}
	if len(d.Anexos) != 2 || d.Anexos[0] != (anexoJSON{"application/pdf", 10, "pdf", false}) || d.Anexos[1].Tamanho != int64(len(grande)) {
		t.Errorf("anexos: %+v", d.Anexos)
	}
	if !bytes.Equal(o.subidos["/storage/"+d.ID+"/0"], []byte("%PDF-1.4 x")) || !bytes.Equal(o.subidos["/storage/"+d.ID+"/1"], grande) {
		t.Errorf("conteúdo que subiu: %d arquivos", len(o.subidos))
	}
	if o.tipos["/storage/"+d.ID+"/0"] != "application/octet-stream" {
		t.Errorf("o PDF subiu como %q", o.tipos["/storage/"+d.ID+"/0"])
	}
	for pedido, a := range o.auth {
		quer := "Bearer segredo"
		if strings.HasPrefix(pedido, "PUT /storage/") {
			quer = "" // o Storage é autorizado pela URL assinada, não pelo token do canal
		}
		if a != quer {
			t.Errorf("%s: Authorization %q", pedido, a)
		}
	}
	rest, _ := st.Listar("")
	if len(rest) != 1 || rest[0].RecebidaEm != "2026-10-28" {
		t.Errorf("no SQLite ficou: %+v", rest)
	}
}

// O JSON entregue só tem os campos da denúncia: nada de quem enviou.
func TestPayloadSoComCamposDaDenuncia(t *testing.T) {
	var campos map[string]any
	b, _ := json.Marshal(denunciaJSON{})
	json.Unmarshal(b, &campos)
	if len(campos) != 5 {
		t.Errorf("campos: %v", campos)
	}
	b, _ = json.Marshal(anexoJSON{})
	campos = nil
	json.Unmarshal(b, &campos)
	if len(campos) != 4 {
		t.Errorf("campos do anexo: %v", campos)
	}
}

func TestAnexoQueOOSJaTemNaoSobeDeNovo(t *testing.T) {
	st, o, e := montar(t)
	registrar(t, st, ontem, store.ArquivoPronto{Dados: []byte("a")}, store.ArquivoPronto{Dados: []byte("b")})
	o.existe[0] = true
	if n, err := e.Tentar(context.Background()); n != 1 || err != nil {
		t.Fatalf("%d %v", n, err)
	}
	if len(o.subidos) != 1 {
		t.Errorf("subiu %d, esperava só o anexo 1", len(o.subidos))
	}
}

func TestAntesDaHoraNaoEntrega(t *testing.T) {
	st, o, e := montar(t)
	registrar(t, st, ontem)
	e.Agora = func() time.Time { return time.Date(2026, 10, 28, 10, 59, 0, 0, time.UTC) } // 07:59 BRT
	if n, err := e.Tentar(context.Background()); n != 0 || err != nil || len(o.pedidos) != 0 {
		t.Errorf("entregou antes da hora: %d %v %v", n, err, o.pedidos)
	}
}

func TestAgoraSaiAntesDaHoraEODiaSeguinteEspera(t *testing.T) {
	st, o, e := montar(t)
	registrar(t, st, ontem)                                // dia seguinte: espera as 8h
	cedo := time.Date(2026, 10, 28, 3, 30, 0, 0, time.UTC) // 00:30 BRT
	if err := st.Registrar(store.Nova{Categoria: "conduta", Descricao: texto, Imediata: true}, nil, cedo); err != nil {
		t.Fatal(err)
	}
	e.Agora = func() time.Time { return cedo }
	if n, err := e.Tentar(context.Background()); n != 1 || err != nil {
		t.Fatalf("entregou %d: %v", n, err)
	}
	if len(o.denuncias) != 1 || o.denuncias[0].Categoria != "conduta" || o.denuncias[0].RecebidaEm != "2026-10-28" {
		t.Errorf("chegou ao OS: %+v", o.denuncias)
	}
	if resto, _ := st.Pendentes("2026-10-29", false); len(resto) != 1 || resto[0].Categoria != "fraude" {
		t.Errorf("a do dia seguinte não esperou: %+v", resto)
	}
}

func TestFalhaMantemNoSQLiteERepete(t *testing.T) {
	for _, recusar := range []string{"POST /api/denuncias/receber", "POST /api/denuncias/receber/", "PUT /storage/"} {
		st, o, e := montar(t)
		registrar(t, st, ontem, store.ArquivoPronto{Dados: []byte("x")})
		o.recusar = recusar

		n, err := e.Tentar(context.Background())
		if n != 0 || err == nil || !strings.Contains(err.Error(), "HTTP 503") || !strings.Contains(err.Error(), "fora do ar") {
			t.Fatalf("recusar %q: %d %v", recusar, n, err)
		}
		if strings.Contains(err.Error(), "token=") {
			t.Errorf("o erro vazou o token da URL assinada: %v", err)
		}
		if rest, _ := st.Listar(""); len(rest) != 1 {
			t.Fatalf("apagou sem confirmação (recusar %q)", recusar)
		}
		o.recusar = ""
		if n, err := e.Tentar(context.Background()); n != 1 || err != nil {
			t.Fatalf("segunda tentativa: %d %v", n, err)
		}
		if rest, _ := st.Listar(""); len(rest) != 0 {
			t.Error("não apagou depois de entregar")
		}
	}
}

func TestRespostaDoAnexoInvalida(t *testing.T) {
	for resposta, quer := range map[string]string{"não é json": "ilegível", `{}`: "para onde subir"} {
		st, o, e := montar(t)
		registrar(t, st, ontem, store.ArquivoPronto{Dados: []byte("x")})
		o.resposta = resposta
		if _, err := e.Tentar(context.Background()); err == nil || !strings.Contains(err.Error(), quer) {
			t.Errorf("%q: %v", resposta, err)
		}
		o.resposta = `{"url":"://quebrada"}`
		if _, err := e.Tentar(context.Background()); err == nil {
			t.Error("URL quebrada devia dar erro")
		}
	}
}

func TestErroDeRede(t *testing.T) {
	st, _, e := montar(t)
	registrar(t, st, ontem)
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
	pendentes, ler, remover error
}

func (f filaQuebrada) Pendentes(string, bool) ([]store.Denuncia, error) {
	return []store.Denuncia{{ID: "x", Anexos: []store.Arquivo{{ID: "a", Tamanho: 1}}}}, f.pendentes
}
func (f filaQuebrada) LerArquivo(_ string, w io.Writer) error {
	if f.ler != nil {
		return f.ler
	}
	_, err := w.Write([]byte("j"))
	return err
}
func (f filaQuebrada) Remover(string) error { return f.remover }

func TestErrosDaFila(t *testing.T) {
	_, _, e := montar(t)
	falha := errors.New("disco")
	for nome, f := range map[string]filaQuebrada{
		"pendentes": {pendentes: falha},
		"ler":       {ler: falha},
		"remover":   {remover: falha},
	} {
		e.Fila = f
		if _, err := e.Tentar(context.Background()); err == nil {
			t.Errorf("%s: esperava erro", nome)
		}
	}
}

func TestRunParaComOContexto(t *testing.T) {
	st, o, e := montar(t)
	registrar(t, st, ontem)
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

func TestSoAFotoLimpaSobeComoImagem(t *testing.T) {
	for tipo, quer := range map[string]string{"image/jpeg": "image/jpeg", "image/gif": "application/octet-stream",
		"text/html; charset=utf-8": "application/octet-stream", "application/pdf": "application/octet-stream"} {
		if got := tipoNoStorage(tipo); got != quer {
			t.Errorf("%s → %s", tipo, got)
		}
	}
}

func TestLimpoChegaAoOS(t *testing.T) {
	st, o, e := montar(t)
	registrar(t, st, ontem, store.ArquivoPronto{Dados: []byte("%PDF-1.4 x"), Extensao: "pdf"}, store.ArquivoPronto{Dados: []byte("bin")})
	list, _ := st.Listar("")
	d, _ := st.Buscar(list[0].ID)
	if err := st.Remendar(d.Anexos[0].ID, nil); err != nil {
		t.Fatal(err)
	}
	if _, err := e.Tentar(context.Background()); err != nil {
		t.Fatal(err)
	}
	if a := o.denuncias[0].Anexos; !a[0].Limpo || a[1].Limpo {
		t.Errorf("anexos: %+v", a)
	}
}
