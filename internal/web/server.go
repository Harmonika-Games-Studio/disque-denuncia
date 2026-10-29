// Package web é a página pública de denúncia e o painel do admin (htmx + templ).
//
// As duas metades moram juntas, mas só o painel pede login. A metade pública
// (publico.go) não lê IP, cookie nem cabeçalho de quem envia, e o servidor não
// grava log de acesso: anonimato_test.go reprova o código que tentar.
package web

import (
	"embed"
	"errors"
	"log/slog"
	"net/http"
	"time"

	"github.com/a-h/templ"

	"disque-denuncia/internal/store"
)

// htmxConfig: respostas 4xx/5xx aparecem no #flash em vez de serem descartadas, e o
// htmx não avalia JavaScript vindo de atributo (a CSP também não deixaria).
const htmxConfig = `{"allowEval":false,"includeIndicatorStyles":false,"responseHandling":[{"code":"204","swap":false},{"code":"[23]..","swap":true},{"code":"[45]..","swap":true,"error":true,"target":"#flash"}]}`

// csp: nada vem de fora (nem fonte, nem script, nem imagem), então o navegador de quem
// denuncia não fala com nenhum outro servidor além deste.
const csp = "default-src 'none'; script-src 'self'; style-src 'self' 'unsafe-inline'; img-src 'self'; " +
	"connect-src 'self'; form-action 'self'; base-uri 'none'; frame-ancestors 'none'"

// O htmx (2.0.4, SHA-384 no README) e o canal.js vêm embutidos no binário. Buscá-los
// num CDN entregaria o IP de quem denuncia a um terceiro.
//
//go:embed static/htmx.min.js static/canal.js
var static embed.FS

// Config é o que a página pública mostra sobre o canal.
type Config struct {
	Empresa string
	// EntregaHora é a hora (Brasília) em que as denúncias vão para o sistema da
	// empresa; -1 = entrega desligada (a administração lê no painel daqui).
	EntregaHora int
	// RepoURL é onde o código do canal está aberto, para qualquer um conferir.
	RepoURL string
}

type Server struct {
	store *store.Store
	cfg   Config
	agora func() time.Time
}

func Handler(s *store.Store, cfg Config) http.Handler {
	srv := &Server{store: s, cfg: cfg, agora: time.Now}
	mux := http.NewServeMux()

	mux.HandleFunc("GET /{$}", srv.formulario)
	mux.HandleFunc("POST /envios", srv.novoEnvio)
	mux.HandleFunc("POST /envios/{id}/arquivos", srv.novoArquivo)
	mux.HandleFunc("PUT /envios/{id}/arquivos/{arquivo}/partes/{n}", srv.gravarParte)
	mux.HandleFunc("POST /envios/{id}/concluir", srv.concluir)
	mux.HandleFunc("POST /denuncias", srv.enviarSemJS)
	mux.Handle("GET /static/", http.FileServerFS(static))

	admin := http.NewServeMux()
	admin.HandleFunc("GET /admin", srv.painel)
	admin.HandleFunc("GET /admin/denuncias/{id}", srv.detalhe)
	admin.HandleFunc("PUT /admin/denuncias/{id}", srv.atualizar)
	admin.HandleFunc("GET /admin/arquivos/{id}", srv.arquivo)
	mux.Handle("/admin", srv.auth(admin))
	mux.Handle("/admin/", srv.auth(admin))

	return cabecalhos(mux)
}

// cabecalhos vale para toda resposta: sem cache (nem no navegador de um computador
// compartilhado), sem Referer para fora, sem câmera/microfone/localização.
func cabecalhos(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h := w.Header()
		h.Set("Content-Security-Policy", csp)
		h.Set("Referrer-Policy", "no-referrer")
		h.Set("Cache-Control", "no-store")
		h.Set("X-Content-Type-Options", "nosniff")
		h.Set("Permissions-Policy", "camera=(), microphone=(), geolocation=(), interest-cohort=()")
		next.ServeHTTP(w, r)
	})
}

// auth exige HTTP Basic com um usuário da tabela users; sem usuário cadastrado, ninguém entra.
func (s *Server) auth(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		user, pass, _ := r.BasicAuth()
		ok, err := s.store.Authenticate(user, pass)
		if err != nil {
			fail(w, r, err)
			return
		}
		if !ok {
			w.Header().Set("WWW-Authenticate", `Basic realm="disque-denuncia", charset="UTF-8"`)
			http.Error(w, "login necessário", http.StatusUnauthorized)
			return
		}
		next.ServeHTTP(w, r)
	})
}

func render(w http.ResponseWriter, r *http.Request, c templ.Component) {
	if err := c.Render(r.Context(), w); err != nil {
		slog.Error("render", "err", err)
	}
}

// fail responde o erro como texto escapado; o htmx joga no #flash. O log de erro
// interno leva só a mensagem do erro: nem caminho, nem IP, nem o conteúdo enviado.
func fail(w http.ResponseWriter, r *http.Request, err error) {
	code, msg := http.StatusInternalServerError, "erro interno; tente de novo"
	var grande *http.MaxBytesError
	switch {
	case errors.Is(err, store.ErrNotFound):
		code, msg = http.StatusNotFound, err.Error()
	case errors.Is(err, store.ErrInvalid), errors.Is(err, errArquivo):
		code, msg = http.StatusBadRequest, err.Error()
	case errors.Is(err, store.ErrLimite):
		code, msg = http.StatusTooManyRequests, err.Error()
	case errors.Is(err, store.ErrCheio):
		code, msg = http.StatusInsufficientStorage, err.Error()
	case errors.As(err, &grande):
		code, msg = http.StatusRequestEntityTooLarge, "o envio passa do tamanho máximo"
	default:
		slog.Error("erro interno", "err", err)
	}
	w.WriteHeader(code)
	render(w, r, Flash(msg))
}
