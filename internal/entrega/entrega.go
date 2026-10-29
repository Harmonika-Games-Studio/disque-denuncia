// Package entrega leva as denúncias para o HarmonikaOS e apaga a cópia local
// depois que o OS confirma.
//
// Por que em lote, e só dias completos: se cada denúncia saísse na hora, o OS
// (logs da Vercel, data do upload, horário do aviso ao admin) saberia a hora do
// envio, que aqui não é guardada. Saindo só as de dias anteriores, a partir de
// HoraEntrega, o horário da entrega não conta nada além da data, e uma nova
// tentativa depois de uma falha pode acontecer a qualquer hora.
//
// Esse é o padrão. Quem denuncia pode escolher "agora" (store.Nova.Imediata): a
// denúncia sai na próxima volta (a cada minuto), sem esperar o dia virar, e a
// página avisa que o horário de chegada no OS mostra a hora aproximada do envio.
//
// O request sai deste servidor: o IP de quem denunciou nunca chega ao OS.
//
// Protocolo (o corpo de uma rota da Vercel não passa de 4,5 MB, e um anexo vai a 5 GB):
//
//	POST {URL}/api/denuncias/receber                  JSON da denúncia e a lista dos anexos (tipo, tamanho, extensão, limpo)
//	POST {URL}/api/denuncias/receber/{id}/anexos/{n}  → {"existe":true} ou {"url": URL assinada de upload}
//	PUT  {url assinada}                               o anexo n, direto no Storage do Supabase
//
// Tudo é idempotente no OS (o id é o desta denúncia, e o anexo que já subiu é
// pulado): se a entrega cair no meio, a próxima repete sem duplicar.
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
	Pendentes(hoje string, soImediatas bool) ([]store.Denuncia, error)
	LerArquivo(id string, w io.Writer) error
	Remover(id string) error
}

type Entregador struct {
	Fila        fila
	URL         string // base do HarmonikaOS, ex.: https://os.harmonikaco.com
	Token       string // Bearer; o mesmo DENUNCIA_TOKEN configurado no OS
	HoraEntrega int    // hora de Brasília a partir da qual o lote do dia seguinte sai
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

// Tentar entrega o que está pendente: as "agora" sempre, as do dia seguinte só depois
// de HoraEntrega. Para na primeira falha (a denúncia que falhou continua no SQLite) e
// devolve quantas foram entregues.
func (e *Entregador) Tentar(ctx context.Context) (int, error) {
	agora := e.Agora()
	pendentes, err := e.Fila.Pendentes(store.Dia(agora), store.Hora(agora) < e.HoraEntrega)
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

type anexoJSON struct {
	Tipo     string `json:"tipo"`
	Tamanho  int64  `json:"tamanho"`
	Extensao string `json:"extensao"`
	Limpo    bool   `json:"limpo"` // os metadados foram tirados aqui
}

type denunciaJSON struct {
	ID         string      `json:"id"`
	Categoria  string      `json:"categoria"`
	Descricao  string      `json:"descricao"`
	RecebidaEm string      `json:"recebida_em"`
	Anexos     []anexoJSON `json:"anexos"`
}

func (e *Entregador) entregar(ctx context.Context, d store.Denuncia) error {
	id := uuid(d.ID)
	anexos := make([]anexoJSON, len(d.Anexos))
	for i, a := range d.Anexos {
		anexos[i] = anexoJSON{a.Tipo, a.Tamanho, a.Extensao, a.Limpo}
	}
	corpo, err := json.Marshal(denunciaJSON{id, d.Categoria, d.Descricao, d.RecebidaEm, anexos})
	if err != nil {
		return err
	}
	if _, err := e.enviar(ctx, "POST", "/api/denuncias/receber", corpo); err != nil {
		return err
	}
	for n, a := range d.Anexos {
		if err := e.entregarAnexo(ctx, id, n, a); err != nil {
			return err
		}
	}
	return nil
}

type destinoJSON struct {
	Existe bool   `json:"existe"`
	URL    string `json:"url"`
}

// entregarAnexo pede ao OS onde subir o anexo n e sobe direto no Storage, lendo do
// SQLite parte por parte (um anexo de 5 GB nunca fica inteiro na memória).
func (e *Entregador) entregarAnexo(ctx context.Context, id string, n int, a store.Arquivo) error {
	resp, err := e.enviar(ctx, "POST", fmt.Sprintf("/api/denuncias/receber/%s/anexos/%d", id, n), nil)
	if err != nil {
		return err
	}
	var destino destinoJSON
	if err := json.Unmarshal(resp, &destino); err != nil {
		return fmt.Errorf("anexo %d: resposta ilegível do OS: %w", n, err)
	}
	if destino.Existe {
		return nil
	}
	if destino.URL == "" {
		return fmt.Errorf("anexo %d: o OS não mandou para onde subir", n)
	}
	leitor, escritor := io.Pipe()
	go func() { escritor.CloseWithError(e.Fila.LerArquivo(a.ID, escritor)) }()
	req, err := http.NewRequestWithContext(ctx, "PUT", destino.URL, leitor)
	if err != nil {
		leitor.Close()
		return err
	}
	req.ContentLength = a.Tamanho
	req.Header.Set("Content-Type", tipoNoStorage(a.Tipo))
	_, err = e.fazer(req)
	leitor.Close()
	return err
}

// tipoNoStorage é o Content-Type com que o Storage vai servir o anexo. Só a foto que
// este canal refez (JPEG sem metadado) vai como imagem, para abrir na tela do admin;
// o resto vai como binário e só baixa: um HTML enviado por qualquer pessoa nunca
// abre no navegador de quem lê.
func tipoNoStorage(tipo string) string {
	if tipo == "image/jpeg" {
		return tipo
	}
	return "application/octet-stream"
}

// enviar chama uma rota do OS (com o token) e devolve o corpo da resposta.
func (e *Entregador) enviar(ctx context.Context, metodo, caminho string, corpo []byte) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, metodo, strings.TrimRight(e.URL, "/")+caminho, bytes.NewReader(corpo))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+e.Token)
	req.Header.Set("Content-Type", "application/json")
	return e.fazer(req)
}

// fazer manda o request e devolve o corpo, ou um erro com o status e o começo da resposta.
func (e *Entregador) fazer(req *http.Request) ([]byte, error) {
	res, err := e.Client.Do(req)
	if err != nil {
		return nil, err
	}
	defer res.Body.Close()
	corpo, _ := io.ReadAll(io.LimitReader(res.Body, 64<<10))
	if res.StatusCode/100 != 2 {
		msg := strings.TrimSpace(string(corpo[:min(len(corpo), 300)]))
		return nil, fmt.Errorf("%s %s: HTTP %d: %s", req.Method, req.URL.Path, res.StatusCode, msg)
	}
	return corpo, nil
}

// uuid formata os 32 hex do id local como uuid (o tipo da coluna no OS).
func uuid(hex string) string {
	if len(hex) != 32 {
		return hex
	}
	return hex[0:8] + "-" + hex[8:12] + "-" + hex[12:16] + "-" + hex[16:20] + "-" + hex[20:32]
}
