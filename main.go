// disque-denuncia: canal anônimo de denúncias. A página pública (/) recebe a
// denúncia sem login; o painel (/admin) pede login (tabela users) e é onde a
// administração lê. Nada que identifique quem denunciou é gravado: ver README.
package main

import (
	"context"
	"io"
	"log"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"time"

	"disque-denuncia/internal/db"
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

	srv := &http.Server{
		Addr:              env("ADDR", "127.0.0.1:8080"),
		Handler:           web.Handler(store.New(conn), env("EMPRESA", "Harmonika")),
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

func env(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}
