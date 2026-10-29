package web

import (
	"fmt"
	"log/slog"
	"net/http"
	"strconv"

	"disque-denuncia/internal/store"
)

// O painel do admin. Tudo aqui passa por auth (HTTP Basic, tabela users).

// painel responde a página inteira, ou só a lista quando é o filtro do htmx.
func (s *Server) painel(w http.ResponseWriter, r *http.Request) {
	status := r.URL.Query().Get("status")
	list, err := s.store.Listar(status)
	if err != nil {
		fail(w, r, err)
		return
	}
	if r.Header.Get("HX-Request") == "true" {
		render(w, r, Lista(list))
		return
	}
	render(w, r, PainelPage(list, status))
}

func (s *Server) detalhe(w http.ResponseWriter, r *http.Request) {
	d, err := s.store.Buscar(r.PathValue("id"))
	if err != nil {
		fail(w, r, err)
		return
	}
	render(w, r, DetalhePage(d))
}

func (s *Server) atualizar(w http.ResponseWriter, r *http.Request) {
	err := s.store.Atualizar(r.PathValue("id"), r.FormValue("status"), r.FormValue("parecer"), s.agora())
	if err != nil {
		fail(w, r, err)
		return
	}
	render(w, r, Flash("Salvo."))
}

// arquivo serve um anexo. Só a imagem já limpa (JPEG refeito aqui) abre no
// navegador; todo o resto vai como download de bytes opacos. Um anexo é conteúdo
// de qualquer pessoa: HTML ou SVG servido inline, na origem do painel, rodaria
// script na sessão do admin.
func (s *Server) arquivo(w http.ResponseWriter, r *http.Request) {
	a, err := s.store.Arquivo(r.PathValue("id"))
	if err != nil {
		fail(w, r, err)
		return
	}
	h := w.Header()
	if a.Tipo == "image/jpeg" {
		h.Set("Content-Type", "image/jpeg")
	} else {
		h.Set("Content-Type", "application/octet-stream")
		h.Set("Content-Disposition", fmt.Sprintf(`attachment; filename="%s"`, nomeDoAnexo(a)))
	}
	h.Set("Content-Length", strconv.FormatInt(a.Tamanho, 10))
	if err := s.store.LerArquivo(a.ID, w); err != nil {
		slog.Error("erro interno", "err", err)
	}
}

// nomeDoAnexo é "anexo-<n>.<extensão>": o nome original nunca chegou aqui.
func nomeDoAnexo(a store.Arquivo) string {
	if a.Extensao == "" {
		return fmt.Sprintf("anexo-%d", a.Posicao+1)
	}
	return fmt.Sprintf("anexo-%d.%s", a.Posicao+1, a.Extensao)
}

func ehImagem(a store.Arquivo) bool { return a.Tipo == "image/jpeg" }

func tamanhoLegivel(b int64) string {
	switch {
	case b >= 1<<20:
		return fmt.Sprintf("%.1f MB", float64(b)/(1<<20))
	case b >= 1<<10:
		return fmt.Sprintf("%d KB", b>>10)
	}
	return fmt.Sprintf("%d bytes", b)
}

func rotuloCategoria(v string) string { return store.Rotulo(store.Categorias, v) }
func rotuloStatus(v string) string    { return store.Rotulo(store.Status, v) }
