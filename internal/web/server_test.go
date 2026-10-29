package web

import (
	"bytes"
	"database/sql"
	"encoding/json"
	"fmt"
	"image"
	"image/color"
	"image/jpeg"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"disque-denuncia/internal/db"
	"disque-denuncia/internal/store"
)

const texto = "Uma descrição com mais de vinte caracteres."

var cfgTeste = Config{Empresa: "Harmonika", EntregaHora: 8, RepoURL: "https://github.com/exemplo/disque-denuncia"}

type ambiente struct {
	h    http.Handler
	conn *sql.DB
	st   *store.Store
	path string
}

// novo sobe o handler num banco temporário com o usuário admin/segredo.
func novo(t *testing.T) ambiente {
	t.Helper()
	path := filepath.Join(t.TempDir(), "t.db")
	conn, err := db.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { conn.Close() })
	_, err = conn.Exec(`INSERT INTO users (username, password_hash) VALUES ('admin', '$2y$04$iOd4Uz.siLSFfamqbwlv3eM7ZRsm4H4gdq3h8Oigbsmcnw3kyoZvu')`)
	if err != nil {
		t.Fatal(err)
	}
	st := store.New(conn)
	return ambiente{Handler(st, cfgTeste), conn, st, path}
}

func (a ambiente) do(t *testing.T, r *http.Request) *httptest.ResponseRecorder {
	t.Helper()
	w := httptest.NewRecorder()
	a.h.ServeHTTP(w, r)
	return w
}

func form(metodo, alvo string, campos url.Values) *http.Request {
	r := httptest.NewRequest(metodo, alvo, strings.NewReader(campos.Encode()))
	r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	return r
}

// enviarComo faz o que o canal.js faz: rascunho, arquivos em partes, concluir.
func (a ambiente) enviarComo(t *testing.T, campos url.Values, arquivos ...[]byte) *httptest.ResponseRecorder {
	t.Helper()
	envio := idDe(t, a.do(t, httptest.NewRequest("POST", "/envios", nil)))
	for _, conteudo := range arquivos {
		w := a.do(t, form("POST", "/envios/"+envio+"/arquivos", url.Values{"tamanho": {fmt.Sprint(len(conteudo))}, "extensao": {"bin"}}))
		arq := idDe(t, w)
		for n := 0; n*store.TamParte < len(conteudo); n++ {
			parte := conteudo[n*store.TamParte : min(len(conteudo), (n+1)*store.TamParte)]
			r := httptest.NewRequest("PUT", fmt.Sprintf("/envios/%s/arquivos/%s/partes/%d", envio, arq, n), bytes.NewReader(parte))
			if w := a.do(t, r); w.Code != 204 {
				t.Fatalf("parte %d: %d %s", n, w.Code, w.Body)
			}
		}
	}
	return a.do(t, form("POST", "/envios/"+envio+"/concluir", campos))
}

func idDe(t *testing.T, w *httptest.ResponseRecorder) string {
	t.Helper()
	var v struct{ ID string }
	if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &v) != nil || v.ID == "" {
		t.Fatalf("esperava {id}: %d %s", w.Code, w.Body)
	}
	return v.ID
}

func validos() url.Values { return url.Values{"categoria": {"fraude"}, "descricao": {texto}} }

// fotoComComentario é um JPEG com um segmento COM ("Fulano de Tal") logo após o SOI.
func fotoComComentario(t *testing.T) []byte {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, 30, 20))
	for i := range img.Pix {
		img.Pix[i] = 200
	}
	img.Set(0, 0, color.Black)
	var b bytes.Buffer
	jpeg.Encode(&b, img, nil)
	j := b.Bytes()
	com := append([]byte{0xFF, 0xFE, 0, byte(len("Fulano de Tal") + 2)}, "Fulano de Tal"...)
	return append(append(append([]byte{}, j[:2]...), com...), j[2:]...)
}

func conferirCabecalhosAnonimos(t *testing.T, w *httptest.ResponseRecorder) {
	t.Helper()
	h := w.Header()
	if h.Get("Set-Cookie") != "" {
		t.Error("resposta pública com cookie")
	}
	if !strings.HasPrefix(h.Get("Content-Security-Policy"), "default-src 'none'") {
		t.Errorf("CSP: %q", h.Get("Content-Security-Policy"))
	}
	if h.Get("Referrer-Policy") != "no-referrer" || h.Get("Cache-Control") != "no-store" {
		t.Errorf("cabeçalhos: %v", h)
	}
}

func TestPaginaPublica(t *testing.T) {
	a := novo(t)
	w := a.do(t, httptest.NewRequest("GET", "/", nil))
	corpo := w.Body.String()
	if w.Code != 200 || !strings.Contains(corpo, `id="form-denuncia"`) || !strings.Contains(corpo, `src="/static/canal.js"`) {
		t.Fatalf("%d %s", w.Code, corpo)
	}
	for _, trecho := range []string{
		"Harmonika", "Assédio moral", "<summary>Saiba mais</summary>", "a partir das 8h",
		cfgTeste.RepoURL, "O que fica:", "Vídeos e áudios do celular", "apagamos os metadados dos arquivos", "até 5 GB no total", `data-max-rotulo="5 GB"`,
	} {
		if !strings.Contains(corpo, trecho) {
			t.Errorf("faltou %q", trecho)
		}
	}
	conferirCabecalhosAnonimos(t, w)

	for _, js := range []string{"/static/htmx.min.js", "/static/canal.js"} {
		if w := a.do(t, httptest.NewRequest("GET", js, nil)); w.Code != 200 || w.Body.Len() < 1000 {
			t.Errorf("%s: %d", js, w.Code)
		}
	}
}

func TestPaginaSemEntregaNemRepositorio(t *testing.T) {
	conn, _ := db.Open(filepath.Join(t.TempDir(), "t.db"))
	defer conn.Close()
	h := Handler(store.New(conn), Config{Empresa: "Dev", EntregaHora: -1})
	w := httptest.NewRecorder()
	h.ServeHTTP(w, httptest.NewRequest("GET", "/", nil))
	corpo := w.Body.String()
	if !strings.Contains(corpo, "ficam guardadas aqui") || strings.Contains(corpo, "GitHub") || strings.Contains(corpo, "a partir das") {
		t.Errorf("sem entrega nem repositório: %s", corpo)
	}
}

func TestEnvioEmPartesComFoto(t *testing.T) {
	a := novo(t)
	grande := bytes.Repeat([]byte("%PDF-1.4 "), store.TamParte/9+10) // passa de uma parte
	w := a.enviarComo(t, validos(), fotoComComentario(t), grande)
	if w.Code != 200 || !strings.Contains(w.Body.String(), "Denúncia recebida") {
		t.Fatalf("%d %s", w.Code, w.Body)
	}
	conferirCabecalhosAnonimos(t, w)

	list, _ := a.st.Listar("")
	d, _ := a.st.Buscar(list[0].ID)
	if len(d.Anexos) != 2 {
		t.Fatalf("anexos: %+v", d.Anexos)
	}
	foto, pdf := d.Anexos[0], d.Anexos[1]
	if foto.Tipo != "image/jpeg" || foto.Extensao != "jpg" || pdf.Tipo != "application/pdf" || pdf.Tamanho != int64(len(grande)) {
		t.Errorf("foto %+v, pdf %+v", foto, pdf)
	}
	var b bytes.Buffer
	a.st.LerArquivo(foto.ID, &b)
	if bytes.Contains(b.Bytes(), []byte("Fulano")) {
		t.Error("a foto guardou o comentário")
	}
}

// Nem o nome do arquivo nem o comentário de dentro da foto chegam ao banco: o
// arquivo do SQLite inteiro é varrido atrás deles.
func TestBancoNaoGuardaNomeNemMetadado(t *testing.T) {
	a := novo(t)
	a.do(t, multipartSemJS(t, validos(), "IMG_Fulano_de_Tal.jpg", fotoComComentario(t)))
	a.conn.Exec(`PRAGMA wal_checkpoint(FULL)`)
	bruto, err := os.ReadFile(a.path)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(bruto, []byte(texto)) {
		t.Fatal("a denúncia nem foi gravada; o teste não prova nada")
	}
	for _, s := range []string{"Fulano", "IMG_"} {
		if bytes.Contains(bruto, []byte(s)) {
			t.Errorf("o banco contém %q", s)
		}
	}
}

func multipartSemJS(t *testing.T, campos url.Values, nome string, dados []byte) *http.Request {
	t.Helper()
	var b bytes.Buffer
	mw := multipart.NewWriter(&b)
	for k, v := range campos {
		mw.WriteField(k, v[0])
	}
	if dados != nil || nome != "" {
		p, _ := mw.CreateFormFile("arquivos", nome)
		p.Write(dados)
	}
	mw.Close()
	r := httptest.NewRequest("POST", "/denuncias", &b)
	r.Header.Set("Content-Type", mw.FormDataContentType())
	return r
}

func TestEnviarSemJavascript(t *testing.T) {
	a := novo(t)
	w := a.do(t, multipartSemJS(t, validos(), "doc.pdf", []byte("%PDF-1.4 conteudo")))
	if w.Code != 200 || !strings.Contains(w.Body.String(), "<html") || !strings.Contains(w.Body.String(), "Denúncia recebida") {
		t.Fatalf("%d %s", w.Code, w.Body)
	}
	list, _ := a.st.Listar("")
	d, _ := a.st.Buscar(list[0].ID)
	if len(d.Anexos) != 1 || d.Anexos[0].Extensao != "pdf" {
		t.Errorf("%+v", d.Anexos)
	}
	// Parte vazia (nenhum arquivo escolhido) é ignorada.
	if w := a.do(t, multipartSemJS(t, validos(), "x", nil)); w.Code != 200 {
		t.Errorf("sem arquivo: %d", w.Code)
	}
}

func TestEnviarSemJavascriptErros(t *testing.T) {
	a := novo(t)
	for nome, tc := range map[string]struct {
		req    *http.Request
		codigo int
		msg    string
	}{
		"categoria":       {multipartSemJS(t, url.Values{"categoria": {"x"}, "descricao": {texto}}, "", nil), 400, "assunto"},
		"foto quebrada":   {multipartSemJS(t, validos(), "a.jpg", fotoComComentario(t)[:200]), 400, "arquivo 1"},
		"grande demais":   {multipartSemJS(t, validos(), "a.bin", make([]byte, maxCorpoSemJS+1)), 413, "tamanho máximo"},
		"não é multipart": {form("POST", "/denuncias", validos()), 400, "ilegível"},
	} {
		w := a.do(t, tc.req)
		if w.Code != tc.codigo || !strings.Contains(w.Body.String(), tc.msg) {
			t.Errorf("%s: %d %q", nome, w.Code, w.Body)
		}
	}
	if list, _ := a.st.Listar(""); len(list) != 0 {
		t.Error("gravou mesmo com erro")
	}
}

func TestExtensaoDoTipo(t *testing.T) {
	for tipo, ext := range map[string]string{"image/jpeg": "jpg", "application/pdf": "pdf", "image/gif": "gif", "video/mp4": "mp4",
		"audio/mpeg": "mp3", "text/plain; charset=utf-8": "txt", "application/zip": ""} {
		if got := extensaoDoTipo(tipo); got != ext {
			t.Errorf("%s → %q", tipo, got)
		}
	}
}

func TestEnvioErros(t *testing.T) {
	a := novo(t)
	envio := idDe(t, a.do(t, httptest.NewRequest("POST", "/envios", nil)))
	arq := idDe(t, a.do(t, form("POST", "/envios/"+envio+"/arquivos", url.Values{"tamanho": {"10"}})))
	parte := func(n string, corpo []byte) *http.Request {
		return httptest.NewRequest("PUT", "/envios/"+envio+"/arquivos/"+arq+"/partes/"+n, bytes.NewReader(corpo))
	}
	for nome, tc := range map[string]struct {
		req    *http.Request
		codigo int
	}{
		"tamanho inválido":         {form("POST", "/envios/"+envio+"/arquivos", url.Values{"tamanho": {"x"}}), 400},
		"rascunho inexistente":     {form("POST", "/envios/nao-existe/arquivos", url.Values{"tamanho": {"1"}}), 404},
		"parte não numérica":       {parte("x", make([]byte, 10)), 400},
		"parte com tamanho errado": {parte("0", make([]byte, 3)), 400},
		"parte grande demais":      {parte("0", make([]byte, store.TamParte+1)), 413},
		"concluir incompleto":      {form("POST", "/envios/"+envio+"/concluir", validos()), 400},
		"concluir sem texto":       {form("POST", "/envios/"+envio+"/concluir", url.Values{}), 400},
		"concluir inexistente":     {form("POST", "/envios/nao-existe/concluir", validos()), 404},
	} {
		if w := a.do(t, tc.req); w.Code != tc.codigo {
			t.Errorf("%s: %d %s", nome, w.Code, w.Body)
		}
	}
}

func TestConcluirRecusaFotoQueNaoAbre(t *testing.T) {
	a := novo(t)
	quebrada := fotoComComentario(t)[:300] // começa como JPEG, mas não decodifica
	w := a.enviarComo(t, validos(), quebrada)
	if w.Code != 400 || !strings.Contains(w.Body.String(), "arquivo 1") {
		t.Errorf("%d %s", w.Code, w.Body)
	}
}

func TestConcluirRecusaFotoGrandeDemaisParaLimpar(t *testing.T) {
	a := novo(t)
	envio := idDe(t, a.do(t, httptest.NewRequest("POST", "/envios", nil)))
	tamanho := maxImagemLimpa + 1
	arq := idDe(t, a.do(t, form("POST", "/envios/"+envio+"/arquivos", url.Values{"tamanho": {fmt.Sprint(tamanho)}})))
	// Só a primeira parte precisa existir: o tipo sai dela, e a recusa vem antes de ler o resto.
	primeira := append(fotoComComentario(t), make([]byte, store.TamParte)...)[:store.TamParte]
	a.do(t, httptest.NewRequest("PUT", "/envios/"+envio+"/arquivos/"+arq+"/partes/0", bytes.NewReader(primeira)))
	w := a.do(t, form("POST", "/envios/"+envio+"/concluir", validos()))
	if w.Code != 400 || !strings.Contains(w.Body.String(), "64 MB") {
		t.Errorf("%d %s", w.Code, w.Body)
	}
}

func TestLimitesDoDia(t *testing.T) {
	a := novo(t)
	for range store.LimitePorDia {
		a.st.Registrar(store.Nova{Categoria: "outro", Descricao: texto}, nil, time.Now())
	}
	if w := a.enviarComo(t, validos()); w.Code != 429 {
		t.Errorf("denúncias: %d %s", w.Code, w.Body)
	}
	for range store.LimiteEnviosPorDia {
		a.st.NovoEnvio(time.Now())
	}
	if w := a.do(t, httptest.NewRequest("POST", "/envios", nil)); w.Code != 429 {
		t.Errorf("rascunhos: %d", w.Code)
	}
}

func TestCanalCheioVira507(t *testing.T) {
	a := novo(t)
	a.st.EspacoMax = 10
	envio := idDe(t, a.do(t, httptest.NewRequest("POST", "/envios", nil)))
	w := a.do(t, form("POST", "/envios/"+envio+"/arquivos", url.Values{"tamanho": {"11"}}))
	if w.Code != http.StatusInsufficientStorage || !strings.Contains(w.Body.String(), "sem espaço") {
		t.Errorf("%d %s", w.Code, w.Body)
	}
}

func TestErroDoBancoVira500SemDetalhe(t *testing.T) {
	a := novo(t)
	a.conn.Close()
	w := a.do(t, httptest.NewRequest("POST", "/envios", nil))
	if w.Code != 500 || !strings.Contains(w.Body.String(), "erro interno") || strings.Contains(w.Body.String(), "sql") {
		t.Errorf("%d %s", w.Code, w.Body)
	}
	if w := a.do(t, form("POST", "/envios/x/concluir", validos())); w.Code != 500 {
		t.Errorf("concluir: %d", w.Code)
	}
	if w := a.do(t, multipartSemJS(t, validos(), "", nil)); w.Code != 500 {
		t.Errorf("sem JS: %d", w.Code)
	}
	r := httptest.NewRequest("GET", "/admin", nil)
	r.SetBasicAuth("admin", "segredo")
	if w := a.do(t, r); w.Code != 500 {
		t.Errorf("auth com banco fechado: %d", w.Code)
	}
}

// --- painel ---

func pedidoAdmin(metodo, alvo string, corpo *strings.Reader) *http.Request {
	if corpo == nil {
		corpo = strings.NewReader("")
	}
	r := httptest.NewRequest(metodo, alvo, corpo)
	r.SetBasicAuth("admin", "segredo")
	if metodo == "PUT" {
		r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	}
	return r
}

func TestAuth(t *testing.T) {
	a := novo(t)
	for _, tc := range []struct {
		nome, alvo, user, pass string
		want                   int
	}{
		{"sem login", "/admin", "", "", 401},
		{"senha errada", "/admin", "admin", "errada", 401},
		{"usuário inexistente", "/admin", "outro", "segredo", 401},
		{"arquivo sem login", "/admin/arquivos/x", "", "", 401},
		{"login certo", "/admin", "admin", "segredo", 200},
	} {
		t.Run(tc.nome, func(t *testing.T) {
			r := httptest.NewRequest("GET", tc.alvo, nil)
			if tc.user != "" {
				r.SetBasicAuth(tc.user, tc.pass)
			}
			w := a.do(t, r)
			if w.Code != tc.want {
				t.Fatalf("status %d, esperava %d", w.Code, tc.want)
			}
			if tc.want == 401 && w.Header().Get("WWW-Authenticate") == "" {
				t.Error("faltou WWW-Authenticate")
			}
		})
	}
}

func TestPainelEDetalhe(t *testing.T) {
	a := novo(t)
	a.enviarComo(t, validos(), fotoComComentario(t), []byte("<html><script>alert(1)</script></html>"))

	w := a.do(t, pedidoAdmin("GET", "/admin", nil))
	if w.Code != 200 || !strings.Contains(w.Body.String(), "Fraude, desvio") || !strings.Contains(w.Body.String(), "<html") {
		t.Fatalf("%d %s", w.Code, w.Body)
	}
	r := pedidoAdmin("GET", "/admin?status=concluida", nil)
	r.Header.Set("HX-Request", "true")
	if w := a.do(t, r); strings.Contains(w.Body.String(), "<html") || !strings.Contains(w.Body.String(), "Nenhuma denúncia") {
		t.Errorf("filtro: %s", w.Body)
	}

	list, _ := a.st.Listar("")
	d, _ := a.st.Buscar(list[0].ID)
	foto, html := d.Anexos[0], d.Anexos[1]
	w = a.do(t, pedidoAdmin("GET", "/admin/denuncias/"+d.ID, nil))
	corpo := w.Body.String()
	if w.Code != 200 || !strings.Contains(corpo, texto) || !strings.Contains(corpo, `<img src="/admin/arquivos/`+foto.ID) ||
		!strings.Contains(corpo, "anexo-2.bin") || !strings.Contains(corpo, "pode conter metadados") {
		t.Errorf("detalhe: %d %s", w.Code, corpo)
	}

	w = a.do(t, pedidoAdmin("GET", "/admin/arquivos/"+foto.ID, nil))
	if w.Code != 200 || w.Header().Get("Content-Type") != "image/jpeg" || w.Header().Get("Content-Disposition") != "" {
		t.Errorf("foto: %d %v", w.Code, w.Header())
	}
	// HTML enviado por qualquer pessoa nunca abre no navegador do admin.
	w = a.do(t, pedidoAdmin("GET", "/admin/arquivos/"+html.ID, nil))
	if w.Header().Get("Content-Type") != "application/octet-stream" ||
		w.Header().Get("Content-Disposition") != `attachment; filename="anexo-2.bin"` || !strings.Contains(w.Body.String(), "<script>") {
		t.Errorf("html: %v", w.Header())
	}
}

func TestNomeETamanhoDoAnexo(t *testing.T) {
	if n := nomeDoAnexo(store.Arquivo{Posicao: 0}); n != "anexo-1" {
		t.Error(n)
	}
	for b, quer := range map[int64]string{10: "10 bytes", 2048: "2 KB", 3 << 20: "3.0 MB"} {
		if got := tamanhoLegivel(b); got != quer {
			t.Errorf("%d → %s", b, got)
		}
	}
}

func TestAtualizar(t *testing.T) {
	a := novo(t)
	a.st.Registrar(store.Nova{Categoria: "outro", Descricao: texto}, nil, time.Now())
	list, _ := a.st.Listar("")
	id := list[0].ID

	w := a.do(t, pedidoAdmin("PUT", "/admin/denuncias/"+id, strings.NewReader("status=concluida&parecer=resolvido")))
	if w.Code != 200 || !strings.Contains(w.Body.String(), "Salvo.") {
		t.Fatalf("%d %s", w.Code, w.Body)
	}
	if w := a.do(t, pedidoAdmin("GET", "/admin/denuncias/"+id, nil)); !strings.Contains(w.Body.String(), "Alterada pela administração") {
		t.Error("detalhe não mostra a alteração")
	}
	if w := a.do(t, pedidoAdmin("PUT", "/admin/denuncias/"+id, strings.NewReader("status=apagada"))); w.Code != 400 {
		t.Errorf("status inválido: %d", w.Code)
	}
}

func TestNaoEncontrado(t *testing.T) {
	a := novo(t)
	for _, r := range []*http.Request{
		pedidoAdmin("GET", "/admin/denuncias/x", nil),
		pedidoAdmin("GET", "/admin/arquivos/x", nil),
		pedidoAdmin("PUT", "/admin/denuncias/x", strings.NewReader("status=nova")),
	} {
		if w := a.do(t, r); w.Code != 404 {
			t.Errorf("%s %s: %d", r.Method, r.URL, w.Code)
		}
	}
}

func TestDataBR(t *testing.T) {
	if dataBR("2026-10-27") != "27/10/2026" || dataBR("ontem") != "ontem" {
		t.Error("dataBR")
	}
}
