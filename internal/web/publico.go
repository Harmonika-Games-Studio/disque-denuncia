package web

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"strconv"

	"disque-denuncia/internal/store"
)

// A metade pública. Daqui só saem os campos que a pessoa preencheu: categoria,
// descrição, o conteúdo dos anexos e a EXTENSÃO de cada um (o navegador nunca manda
// o nome do arquivo, que pode ter o nome da pessoa).
//
// Com JavaScript (static/canal.js), o envio vai em pedaços:
//
//	POST /envios                                  abre o rascunho → {"id"}
//	POST /envios/{id}/arquivos                    tamanho, extensao → {"id"}
//	PUT  /envios/{id}/arquivos/{arquivo}/partes/{n}  até 8 MB por parte
//	POST /envios/{id}/concluir                    categoria, descricao
//
// Sem JavaScript, o formulário comum vai para POST /denuncias de uma vez, com anexos
// pequenos (precisa caber nos 60 s do proxy).

const (
	// maxCorpoSemJS é o formulário comum inteiro; cabe na memória (maxMemoria = maxCorpo:
	// o net/http gravaria em arquivo temporário o que passasse, e arquivo tem hora de criação).
	maxCorpoSemJS = 20 << 20
)

func (s *Server) formulario(w http.ResponseWriter, r *http.Request) {
	render(w, r, FormularioPage(s.cfg))
}

func (s *Server) novoEnvio(w http.ResponseWriter, r *http.Request) {
	id, err := s.store.NovoEnvio(s.agora())
	if err != nil {
		fail(w, r, err)
		return
	}
	responderJSON(w, map[string]any{"id": id, "tamParte": store.TamParte})
}

func (s *Server) novoArquivo(w http.ResponseWriter, r *http.Request) {
	tamanho, err := strconv.ParseInt(r.FormValue("tamanho"), 10, 64)
	if err != nil {
		fail(w, r, fmt.Errorf("%w: tamanho inválido", store.ErrInvalid))
		return
	}
	id, err := s.store.NovoArquivo(r.PathValue("id"), tamanho, r.FormValue("extensao"))
	if err != nil {
		fail(w, r, err)
		return
	}
	responderJSON(w, map[string]any{"id": id})
}

func (s *Server) gravarParte(w http.ResponseWriter, r *http.Request) {
	n, err := strconv.Atoi(r.PathValue("n"))
	if err != nil {
		fail(w, r, fmt.Errorf("%w: parte inválida", store.ErrInvalid))
		return
	}
	dados, err := io.ReadAll(http.MaxBytesReader(w, r.Body, store.TamParte))
	if err != nil {
		fail(w, r, err)
		return
	}
	if err := s.store.GravarParte(r.PathValue("id"), r.PathValue("arquivo"), n, dados); err != nil {
		fail(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) concluir(w http.ResponseWriter, r *http.Request) {
	envio := r.PathValue("id")
	n := store.Nova{Categoria: r.FormValue("categoria"), Descricao: r.FormValue("descricao")}
	// Valida o texto antes de gastar CPU com os anexos.
	if err := n.Validar(); err != nil {
		fail(w, r, err)
		return
	}
	if err := s.limparEnvio(envio); err != nil {
		fail(w, r, err)
		return
	}
	if err := s.store.Concluir(envio, n, s.agora()); err != nil {
		fail(w, r, err)
		return
	}
	render(w, r, Recebida())
}

// enviarSemJS é o formulário comum: tudo numa requisição, com anexos pequenos.
func (s *Server) enviarSemJS(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, maxCorpoSemJS)
	if err := r.ParseMultipartForm(maxCorpoSemJS); err != nil {
		fail(w, r, corpoInvalido(err))
		return
	}
	defer r.MultipartForm.RemoveAll()

	n := store.Nova{Categoria: r.FormValue("categoria"), Descricao: r.FormValue("descricao")}
	if err := n.Validar(); err != nil {
		fail(w, r, err)
		return
	}
	anexos, err := prontos(r.MultipartForm.File["arquivos"])
	if err != nil {
		fail(w, r, err)
		return
	}
	envio, err := s.store.Rascunho(anexos, s.agora())
	if err != nil {
		fail(w, r, err)
		return
	}
	if err := s.limparEnvio(envio); err != nil {
		s.store.Descartar(envio)
		fail(w, r, err)
		return
	}
	if err := s.store.Concluir(envio, n, s.agora()); err != nil {
		s.store.Descartar(envio)
		fail(w, r, err)
		return
	}
	render(w, r, RecebidaPage(s.cfg))
}

// prontos lê os anexos do formulário comum (a limpeza vem depois, igual ao envio em
// pedaços). Sem JavaScript não há como mandar só a extensão, e o nome inteiro nunca é
// lido: a extensão sai do tipo.
func prontos(fs []*multipart.FileHeader) ([]store.ArquivoPronto, error) {
	var out []store.ArquivoPronto
	for _, f := range fs {
		if f.Size == 0 {
			continue // a parte vazia que o navegador manda sem arquivo escolhido
		}
		dados, err := ler(f)
		if err != nil {
			return nil, err
		}
		out = append(out, store.ArquivoPronto{Dados: dados, Extensao: extensaoDoTipo(http.DetectContentType(dados))})
	}
	return out, nil
}

func extensaoDoTipo(tipo string) string {
	switch tipo {
	case "image/jpeg":
		return "jpg"
	case "application/pdf":
		return "pdf"
	case "image/gif":
		return "gif"
	case "video/mp4":
		return "mp4"
	case "audio/mpeg":
		return "mp3"
	case "text/plain; charset=utf-8":
		return "txt"
	}
	return ""
}

func ler(f *multipart.FileHeader) ([]byte, error) {
	arq, err := f.Open()
	if err != nil {
		return nil, err
	}
	defer arq.Close()
	return io.ReadAll(arq)
}

func corpoInvalido(err error) error {
	var grande *http.MaxBytesError
	if errors.As(err, &grande) {
		return err
	}
	return fmt.Errorf("%w: formulário ilegível", store.ErrInvalid)
}

func responderJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(v)
}
