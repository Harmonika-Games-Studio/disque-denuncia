// disque-denuncia: canal anônimo de denúncias. A página pública (/) recebe a
// denúncia sem login; uma vez por dia o lote dos dias anteriores vai para o
// HarmonikaOS (pacote entrega) e sai daqui. O painel (/admin, com login) mostra o
// que ainda não foi entregue. Nada que identifique quem denunciou é gravado: ver README.
package main

import (
	"context"
	"errors"
	"io"
	"log"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"time"

	"disque-denuncia/internal/db"
	"disque-denuncia/internal/entrega"
	"disque-denuncia/internal/store"
	"disque-denuncia/internal/web"
)

func main() {
	if err := run(); err != nil {
		slog.Error("fatal", "err", err)
		os.Exit(1)
	}
}

func run() error {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()

	conn, err := db.Open(env("DB_PATH", "denuncias.db"))
	if err != nil {
		return err
	}
	defer conn.Close()
	st := store.New(conn)
	gb, err := strconv.ParseInt(env("ESPACO_MAX_GB", "50"), 10, 64)
	if err != nil || gb < 1 {
		return errors.New("ESPACO_MAX_GB deve ser um número de GB, 1 ou mais")
	}
	st.EspacoMax = gb << 30

	horaEntrega, err := iniciarEntrega(ctx, st)
	if err != nil {
		return err
	}
	go limparRascunhos(ctx, st)

	cfg := web.Config{
		Empresa:     env("EMPRESA", "Harmonika"),
		EntregaHora: horaEntrega,
		RepoURL:     env("REPO_URL", "https://github.com/Harmonika-Games-Studio/disque-denuncia"),
	}
	srv := &http.Server{
		Addr:              env("ADDR", "127.0.0.1:8080"),
		Handler:           web.Handler(st, cfg),
		ReadHeaderTimeout: 10 * time.Second,
		ReadTimeout:       2 * time.Minute, // uma parte de 8 MB (ou o formulário sem JavaScript) em rede lenta
		WriteTimeout:      2 * time.Minute,
		// O log interno do net/http escreve o IP do cliente ("http: panic serving
		// 1.2.3.4:5678", erros de TLS). Vai para o vazio.
		ErrorLog: log.New(io.Discard, "", 0),
	}
	go func() {
		<-ctx.Done()
		srv.Shutdown(context.Background())
	}()
	slog.Info("no ar", "addr", srv.Addr)
	if err := srv.ListenAndServe(); err != http.ErrServerClosed {
		return err
	}
	return nil
}

// iniciarEntrega liga a entrega diária ao HarmonikaOS e devolve a hora dela. Sem
// OS_URL, as denúncias ficam só aqui, no painel /admin, e a hora é -1.
func iniciarEntrega(ctx context.Context, st *store.Store) (int, error) {
	url, token := os.Getenv("OS_URL"), os.Getenv("OS_TOKEN")
	if url == "" {
		slog.Warn("OS_URL vazio: entrega ao HarmonikaOS desligada; as denúncias ficam no painel /admin")
		return -1, nil
	}
	if token == "" {
		return 0, errors.New("OS_URL sem OS_TOKEN")
	}
	hora, err := strconv.Atoi(env("ENTREGA_HORA", "8"))
	if err != nil || hora < 0 || hora > 23 {
		return 0, errors.New("ENTREGA_HORA deve ser uma hora de 0 a 23")
	}
	// Sem timeout total no cliente: um anexo de centenas de MB leva minutos para
	// subir. Quem limita é o tempo de conexão e de resposta.
	cliente := &http.Client{Transport: &http.Transport{
		Proxy: http.ProxyFromEnvironment, TLSHandshakeTimeout: 30 * time.Second,
		ResponseHeaderTimeout: 2 * time.Minute,
	}}
	e := &entrega.Entregador{
		Fila: st, URL: url, Token: token, HoraEntrega: hora, Client: cliente, Agora: time.Now,
	}
	go e.Run(ctx, 10*time.Minute)
	slog.Info("entrega ao HarmonikaOS ligada", "url", url, "a_partir_das", hora)
	return hora, nil
}

// limparRascunhos apaga, de hora em hora, os envios abandonados no meio.
func limparRascunhos(ctx context.Context, st *store.Store) {
	t := time.NewTicker(time.Hour)
	defer t.Stop()
	for {
		if _, err := st.LimparRascunhos(time.Now()); err != nil {
			slog.Error("limpeza de rascunhos falhou", "err", err)
		}
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
	}
}

func env(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}
