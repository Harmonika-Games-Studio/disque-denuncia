package web

import (
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

func (s *Server) anexo(w http.ResponseWriter, r *http.Request) {
	jpeg, err := s.store.Anexo(r.PathValue("id"))
	if err != nil {
		fail(w, r, err)
		return
	}
	w.Header().Set("Content-Type", "image/jpeg")
	w.Header().Set("Content-Length", strconv.Itoa(len(jpeg)))
	w.Write(jpeg)
}

func rotuloCategoria(v string) string { return store.Rotulo(store.Categorias, v) }
func rotuloStatus(v string) string    { return store.Rotulo(store.Status, v) }
