// donation-service: processamento das doações da SolidaryTech (Hot Path).
//
// POST /donations grava a doação no PostgreSQL e publica um evento no SQS.
// É o serviço crítico da plataforma: os SLIs/SLOs de disponibilidade e
// latência são medidos nele (ver docs/sre-slo.md no repositório de GitOps).
package main

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/joho/godotenv"
	"go.opentelemetry.io/contrib/instrumentation/net/http/otelhttp"
)

func main() {
	_ = godotenv.Load()
	setupLogger()

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	shutdownTelemetry, err := setupTelemetry(ctx)
	if err != nil {
		slog.Warn("falha ao iniciar o OpenTelemetry", "erro", err)
	}
	defer func() {
		c, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = shutdownTelemetry(c)
	}()

	dbURL := os.Getenv("DATABASE_URL")
	if dbURL == "" {
		slog.Error("DATABASE_URL é obrigatória")
		os.Exit(1)
	}
	store, err := NewPostgresStore(ctx, dbURL)
	if err != nil {
		slog.Error("erro ao conectar ao PostgreSQL", "erro", err)
		os.Exit(1)
	}
	slog.Info("conectado ao PostgreSQL (donation-service)")

	var publisher Publisher = noopPublisher{}
	if queueURL, region := os.Getenv("AWS_SQS_URL"), os.Getenv("AWS_REGION"); queueURL != "" && region != "" {
		p, err := NewSQSPublisher(ctx, region, queueURL)
		if err != nil {
			slog.Warn("SQS indisponível: eventos de doação não serão publicados", "erro", err)
		} else {
			publisher = p
			slog.Info("integração com AWS SQS ativada", "fila", queueURL)
		}
	}

	app := NewApp(store, publisher, os.Getenv("ADMIN_TOKEN"))

	port := os.Getenv("PORT")
	if port == "" {
		port = "8082"
	}
	srv := &http.Server{
		Addr:              ":" + port,
		Handler:           otelhttp.NewHandler(app.Routes(), "donation-service", otelhttp.WithFilter(isBusinessRoute), otelhttp.WithSpanNameFormatter(spanName)),
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       10 * time.Second,
		WriteTimeout:      15 * time.Second,
		IdleTimeout:       60 * time.Second,
	}

	go func() {
		slog.Info("donation-service rodando", "porta", port)
		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			slog.Error("servidor HTTP parou", "erro", err)
			stop()
		}
	}()

	<-ctx.Done()
	// Graceful shutdown: termina as doações em andamento antes de o pod sair
	// (rollout e scale-down não derrubam requisições = não queimam error budget).
	slog.Info("desligando: aguardando requisições em andamento")
	c, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	_ = srv.Shutdown(c)
	store.Close()
}

// Probes não viram span nem métrica: senão o SLO seria inflado por /health.
func isBusinessRoute(r *http.Request) bool {
	return r.URL.Path != "/health" && r.URL.Path != "/ready"
}

func spanName(_ string, r *http.Request) string {
	return r.Method + " " + r.URL.Path
}
