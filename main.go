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

	if err := iniciarEntrega(ctx, st); err != nil {
		return err
	}

	srv := &http.Server{
		Addr:              env("ADDR", "127.0.0.1:8080"),
		Handler:           web.Handler(st, env("EMPRESA", "Harmonika")),
		ReadHeaderTimeout: 10 * time.Second,
		ReadTimeout:       2 * time.Minute, // upload de até 3 imagens em rede lenta
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

// iniciarEntrega liga a entrega diária ao HarmonikaOS. Sem OS_URL, as denúncias
// ficam só aqui, no painel /admin.
func iniciarEntrega(ctx context.Context, st *store.Store) error {
	url, token := os.Getenv("OS_URL"), os.Getenv("OS_TOKEN")
	if url == "" {
		slog.Warn("OS_URL vazio: entrega ao HarmonikaOS desligada; as denúncias ficam no painel /admin")
		return nil
	}
	if token == "" {
		return errors.New("OS_URL sem OS_TOKEN")
	}
	hora, err := strconv.Atoi(env("ENTREGA_HORA", "8"))
	if err != nil || hora < 0 || hora > 23 {
		return errors.New("ENTREGA_HORA deve ser uma hora de 0 a 23")
	}
	e := &entrega.Entregador{
		Fila: st, URL: url, Token: token, HoraEntrega: hora,
		Client: &http.Client{Timeout: time.Minute}, Agora: time.Now,
	}
	go e.Run(ctx, 10*time.Minute)
	slog.Info("entrega ao HarmonikaOS ligada", "url", url, "a_partir_das", hora)
	return nil
}

func env(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}
