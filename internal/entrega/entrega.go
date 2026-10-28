// Package entrega leva as denúncias para o HarmonikaOS uma vez por dia e apaga
// a cópia local depois que o OS confirma.
//
// Por que em lote, e só dias completos: se cada denúncia saísse na hora, o OS
// (logs da Vercel, data do upload, horário do aviso ao admin) saberia a hora do
// envio, que aqui não é guardada. Saindo só as de dias anteriores, a partir de
// HoraEntrega, o horário da entrega não conta nada além da data, e uma nova
// tentativa depois de uma falha pode acontecer a qualquer hora.
//
// O request sai deste servidor: o IP de quem denunciou nunca chega ao OS.
//
// Protocolo (cada request cabe no limite de 4,5 MB da Vercel):
//
//	POST {URL}/api/denuncias/receber                   JSON da denúncia, sem imagens
//	PUT  {URL}/api/denuncias/receber/{id}/imagens/{n}  o JPEG n (0, 1, 2)
//
// Os dois são idempotentes no OS (o id é o desta denúncia): se a entrega cair no
// meio, a próxima repete tudo sem duplicar.
package entrega

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"disque-denuncia/internal/store"
)

// fila é o que a entrega precisa do store.
type fila interface {
	Pendentes(hoje string) ([]store.Denuncia, error)
	Anexo(id string) ([]byte, error)
	Remover(id string) error
}

type Entregador struct {
	Fila        fila
	URL         string // base do HarmonikaOS, ex.: https://os.harmonikaco.com
	Token       string // Bearer; o mesmo DENUNCIA_TOKEN configurado no OS
	HoraEntrega int    // hora de Brasília a partir da qual o lote do dia sai
	Client      *http.Client
	Agora       func() time.Time
}

// Run tenta a entrega a cada intervalo até o contexto acabar.
func (e *Entregador) Run(ctx context.Context, intervalo time.Duration) {
	t := time.NewTicker(intervalo)
	defer t.Stop()
	for {
		if n, err := e.Tentar(ctx); err != nil {
			slog.Error("entrega ao HarmonikaOS falhou; tenta de novo no próximo ciclo", "entregues", n, "err", err)
		} else if n > 0 {
			slog.Info("denúncias entregues ao HarmonikaOS", "quantidade", n)
		}
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
	}
}

// Tentar entrega o que está pendente, se já passou da hora. Para na primeira falha
// (a denúncia que falhou continua no SQLite) e devolve quantas foram entregues.
func (e *Entregador) Tentar(ctx context.Context) (int, error) {
	agora := e.Agora()
	if store.Hora(agora) < e.HoraEntrega {
		return 0, nil
	}
	pendentes, err := e.Fila.Pendentes(store.Dia(agora))
	if err != nil {
		return 0, err
	}
	for i, d := range pendentes {
		if err := e.entregar(ctx, d); err != nil {
			return i, err
		}
		if err := e.Fila.Remover(d.ID); err != nil {
			return i, err
		}
	}
	return len(pendentes), nil
}

type denunciaJSON struct {
	ID           string `json:"id"`
	Categoria    string `json:"categoria"`
	Descricao    string `json:"descricao"`
	RecebidaEm   string `json:"recebida_em"`
	TotalImagens int    `json:"total_imagens"`
}

func (e *Entregador) entregar(ctx context.Context, d store.Denuncia) error {
	id := uuid(d.ID)
	corpo, err := json.Marshal(denunciaJSON{id, d.Categoria, d.Descricao, d.RecebidaEm, len(d.Anexos)})
	if err != nil {
		return err
	}
	if err := e.enviar(ctx, "POST", "/api/denuncias/receber", "application/json", corpo); err != nil {
		return err
	}
	for n, anexo := range d.Anexos {
		jpeg, err := e.Fila.Anexo(anexo)
		if err != nil {
			return err
		}
		caminho := fmt.Sprintf("/api/denuncias/receber/%s/imagens/%d", id, n)
		if err := e.enviar(ctx, "PUT", caminho, "image/jpeg", jpeg); err != nil {
			return err
		}
	}
	return nil
}

func (e *Entregador) enviar(ctx context.Context, metodo, caminho, tipo string, corpo []byte) error {
	req, err := http.NewRequestWithContext(ctx, metodo, strings.TrimRight(e.URL, "/")+caminho, bytes.NewReader(corpo))
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+e.Token)
	req.Header.Set("Content-Type", tipo)
	res, err := e.Client.Do(req)
	if err != nil {
		return err
	}
	defer res.Body.Close()
	if res.StatusCode/100 != 2 {
		msg, _ := io.ReadAll(io.LimitReader(res.Body, 300))
		return fmt.Errorf("%s %s: HTTP %d: %s", metodo, caminho, res.StatusCode, strings.TrimSpace(string(msg)))
	}
	return nil
}

// uuid formata os 32 hex do id local como uuid (o tipo da coluna no OS).
func uuid(hex string) string {
	if len(hex) != 32 {
		return hex
	}
	return hex[0:8] + "-" + hex[8:12] + "-" + hex[12:16] + "-" + hex[16:20] + "-" + hex[20:32]
}
