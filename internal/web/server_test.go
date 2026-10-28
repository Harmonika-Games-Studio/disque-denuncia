package web

import (
	"bytes"
	"database/sql"
	"image"
	"image/color"
	"image/jpeg"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"disque-denuncia/internal/db"
	"disque-denuncia/internal/store"
)

const texto = "Uma descrição com mais de vinte caracteres."

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
	return ambiente{Handler(st, "Harmonika"), conn, st, path}
}

func (a ambiente) do(t *testing.T, r *http.Request) *httptest.ResponseRecorder {
	t.Helper()
	w := httptest.NewRecorder()
	a.h.ServeHTTP(w, r)
	return w
}

type arquivo struct {
	nome, tipo string
	dados      []byte
}

// formulario monta o multipart da página pública.
func formulario(t *testing.T, campos map[string]string, arquivos ...arquivo) *http.Request {
	t.Helper()
	var b bytes.Buffer
	mw := multipart.NewWriter(&b)
	for k, v := range campos {
		mw.WriteField(k, v)
	}
	for _, a := range arquivos {
		h := make(map[string][]string)
		h["Content-Disposition"] = []string{`form-data; name="imagens"; filename="` + a.nome + `"`}
		h["Content-Type"] = []string{a.tipo}
		p, _ := mw.CreatePart(h)
		p.Write(a.dados)
	}
	mw.Close()
	r := httptest.NewRequest("POST", "/denuncias", &b)
	r.Header.Set("Content-Type", mw.FormDataContentType())
	return r
}

// jpegComComentario é uma foto com um segmento COM ("Fulano de Tal") logo após o SOI.
func jpegComComentario(t *testing.T) []byte {
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

func validos() map[string]string {
	return map[string]string{"categoria": "fraude", "descricao": texto}
}

func TestPaginaPublica(t *testing.T) {
	a := novo(t)
	w := a.do(t, httptest.NewRequest("GET", "/", nil))
	if w.Code != 200 || !strings.Contains(w.Body.String(), `hx-post="/denuncias"`) {
		t.Fatalf("%d %s", w.Code, w.Body)
	}
	if !strings.Contains(w.Body.String(), "Harmonika") || !strings.Contains(w.Body.String(), "Assédio moral") {
		t.Error("faltou empresa ou categorias")
	}
	conferirCabecalhosAnonimos(t, w)

	w = a.do(t, httptest.NewRequest("GET", "/static/htmx.min.js", nil))
	if w.Code != 200 || !strings.HasPrefix(w.Body.String(), "var htmx") {
		t.Errorf("htmx embutido: %d", w.Code)
	}
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

func TestEnviarComHtmx(t *testing.T) {
	a := novo(t)
	r := formulario(t, validos(), arquivo{"IMG_Fulano_de_Tal.jpg", "image/png", jpegComComentario(t)})
	r.Header.Set("HX-Request", "true")
	w := a.do(t, r)
	if w.Code != 200 || !strings.Contains(w.Body.String(), "Denúncia recebida") || strings.Contains(w.Body.String(), "<html") {
		t.Fatalf("%d %s", w.Code, w.Body)
	}
	conferirCabecalhosAnonimos(t, w)

	list, _ := a.st.Listar("")
	if len(list) != 1 || len(list[0].Anexos) != 1 {
		t.Fatalf("gravado: %+v", list)
	}
	d, _ := a.st.Buscar(list[0].ID)
	img, _ := a.st.Anexo(d.Anexos[0])
	if _, f, err := image.Decode(bytes.NewReader(img)); err != nil || f != "jpeg" {
		t.Errorf("anexo não é JPEG: %s %v", f, err)
	}
}

// Nem o nome do arquivo nem o comentário de dentro da foto chegam ao banco: o
// arquivo do SQLite inteiro é varrido atrás deles.
func TestBancoNaoGuardaNomeNemMetadado(t *testing.T) {
	a := novo(t)
	a.do(t, formulario(t, validos(), arquivo{"IMG_Fulano_de_Tal.jpg", "image/jpeg", jpegComComentario(t)}))
	a.conn.Exec(`PRAGMA wal_checkpoint(FULL)`)
	bruto, err := os.ReadFile(a.path)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(bruto, []byte(texto)) {
		t.Fatal("a denúncia nem foi gravada; o teste não prova nada")
	}
	for _, s := range []string{"Fulano", "IMG_", "image/png"} {
		if bytes.Contains(bruto, []byte(s)) {
			t.Errorf("o banco contém %q", s)
		}
	}
}

func TestEnviarSemJavascript(t *testing.T) {
	a := novo(t)
	w := a.do(t, formulario(t, validos()))
	if w.Code != 200 || !strings.Contains(w.Body.String(), "<html") || !strings.Contains(w.Body.String(), "Denúncia recebida") {
		t.Fatalf("%d %s", w.Code, w.Body)
	}
}

func TestEnviarIgnoraParteVazia(t *testing.T) {
	a := novo(t)
	w := a.do(t, formulario(t, validos(), arquivo{"", "application/octet-stream", nil}))
	if w.Code != 200 {
		t.Fatalf("%d %s", w.Code, w.Body)
	}
	list, _ := a.st.Listar("")
	if len(list) != 1 || len(list[0].Anexos) != 0 {
		t.Errorf("%+v", list)
	}
}

func TestEnviarErros(t *testing.T) {
	foto := arquivo{"a.jpg", "image/jpeg", jpegComComentario(t)}
	grande := arquivo{"g.jpg", "image/jpeg", make([]byte, maxImagem+1)}
	for _, tc := range []struct {
		nome   string
		req    func(t *testing.T) *http.Request
		codigo int
		msg    string
	}{
		{"categoria inválida", func(t *testing.T) *http.Request {
			return formulario(t, map[string]string{"categoria": "x", "descricao": texto})
		}, 400, "assunto"},
		{"descrição curta", func(t *testing.T) *http.Request {
			return formulario(t, map[string]string{"categoria": "outro", "descricao": "curta"})
		}, 400, "pelo menos"},
		{"imagens demais", func(t *testing.T) *http.Request { return formulario(t, validos(), foto, foto, foto, foto) }, 400, "no máximo"},
		{"não é imagem", func(t *testing.T) *http.Request {
			return formulario(t, validos(), arquivo{"a.jpg", "image/jpeg", []byte("texto")})
		}, 400, "imagem 1"},
		{"imagem grande", func(t *testing.T) *http.Request { return formulario(t, validos(), grande) }, 400, "8 MB"},
		{"corpo grande", func(t *testing.T) *http.Request { return formulario(t, validos(), grande, grande, grande, grande) }, 413, "tamanho máximo"},
		{"não é multipart", func(t *testing.T) *http.Request {
			return httptest.NewRequest("POST", "/denuncias", strings.NewReader("categoria=outro"))
		}, 400, "ilegível"},
	} {
		t.Run(tc.nome, func(t *testing.T) {
			a := novo(t)
			w := a.do(t, tc.req(t))
			if w.Code != tc.codigo || !strings.Contains(w.Body.String(), tc.msg) {
				t.Errorf("%d %q", w.Code, w.Body)
			}
			if list, _ := a.st.Listar(""); len(list) != 0 {
				t.Error("gravou mesmo com erro")
			}
		})
	}
}

func TestEnviarLimiteDoDia(t *testing.T) {
	a := novo(t)
	for range store.LimitePorDia {
		a.st.Registrar(store.Nova{Categoria: "outro", Descricao: texto}, time.Now())
	}
	if w := a.do(t, formulario(t, validos())); w.Code != 429 {
		t.Errorf("%d %s", w.Code, w.Body)
	}
}

func TestErroDoBancoVira500SemDetalhe(t *testing.T) {
	a := novo(t)
	a.conn.Close()
	w := a.do(t, formulario(t, validos()))
	if w.Code != 500 || !strings.Contains(w.Body.String(), "erro interno") || strings.Contains(w.Body.String(), "sql") {
		t.Errorf("%d %s", w.Code, w.Body)
	}
	r := httptest.NewRequest("GET", "/admin", nil)
	r.SetBasicAuth("admin", "segredo")
	if w := a.do(t, r); w.Code != 500 {
		t.Errorf("auth com banco fechado: %d", w.Code)
	}
}

// --- painel ---

func pedidoAdmin(method, alvo string, body *strings.Reader) *http.Request {
	if body == nil {
		body = strings.NewReader("")
	}
	r := httptest.NewRequest(method, alvo, body)
	r.SetBasicAuth("admin", "segredo")
	if method == "PUT" {
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
		{"anexo sem login", "/admin/anexos/x", "", "", 401},
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
	a.do(t, formulario(t, validos(), arquivo{"a.jpg", "image/jpeg", jpegComComentario(t)}))
	a.st.Registrar(store.Nova{Categoria: "seguranca", Descricao: texto}, time.Now())

	w := a.do(t, pedidoAdmin("GET", "/admin", nil))
	if w.Code != 200 || !strings.Contains(w.Body.String(), "Fraude, desvio") || !strings.Contains(w.Body.String(), store.Dia(time.Now())[8:10]+"/") {
		t.Fatalf("%d %s", w.Code, w.Body)
	}
	if !strings.Contains(w.Body.String(), "<html") {
		t.Error("painel sem página inteira")
	}

	r := pedidoAdmin("GET", "/admin?status=concluida", nil)
	r.Header.Set("HX-Request", "true")
	w = a.do(t, r)
	if w.Code != 200 || strings.Contains(w.Body.String(), "<html") || !strings.Contains(w.Body.String(), "Nenhuma denúncia") {
		t.Errorf("filtro: %s", w.Body)
	}

	list, _ := a.st.Listar("")
	var comFoto store.Denuncia
	for _, d := range list {
		if d.Categoria == "fraude" {
			comFoto, _ = a.st.Buscar(d.ID)
		}
	}
	w = a.do(t, pedidoAdmin("GET", "/admin/denuncias/"+comFoto.ID, nil))
	if w.Code != 200 || !strings.Contains(w.Body.String(), texto) || !strings.Contains(w.Body.String(), "/admin/anexos/"+comFoto.Anexos[0]) {
		t.Errorf("detalhe: %d %s", w.Code, w.Body)
	}

	w = a.do(t, pedidoAdmin("GET", "/admin/anexos/"+comFoto.Anexos[0], nil))
	if w.Code != 200 || w.Header().Get("Content-Type") != "image/jpeg" || w.Body.Len() == 0 {
		t.Errorf("anexo: %d %v", w.Code, w.Header())
	}
}

func TestAtualizar(t *testing.T) {
	a := novo(t)
	a.st.Registrar(store.Nova{Categoria: "outro", Descricao: texto}, time.Now())
	list, _ := a.st.Listar("")
	id := list[0].ID

	w := a.do(t, pedidoAdmin("PUT", "/admin/denuncias/"+id, strings.NewReader("status=concluida&parecer=resolvido")))
	if w.Code != 200 || !strings.Contains(w.Body.String(), "Salvo.") {
		t.Fatalf("%d %s", w.Code, w.Body)
	}
	d, _ := a.st.Buscar(id)
	if d.Status != "concluida" || d.Parecer != "resolvido" {
		t.Errorf("%+v", d)
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
		pedidoAdmin("GET", "/admin/anexos/x", nil),
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
