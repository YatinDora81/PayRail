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

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/payrail/go/internal/config"
	"github.com/payrail/go/internal/gatewayclient"
	"github.com/payrail/go/internal/telemetry"
	"github.com/payrail/go/internal/webhookingest"
	"go.opentelemetry.io/contrib/instrumentation/net/http/otelhttp"
)

func main() {
	base := slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelInfo})
	logger := slog.New(telemetry.NewSlogHandler(base))
	slog.SetDefault(logger)
	if err := run(logger); err != nil {
		logger.Error("webhook-ingest failed", "err", err)
		os.Exit(1)
	}
}

func run(logger *slog.Logger) error {
	cfg, err := config.LoadWebhook()
	if err != nil {
		return err
	}

	ctx := context.Background()
	shutdown, err := telemetry.Init(ctx, "webhook-ingest")

	defer func() {
		_ = shutdown(context.Background())
	}()

	pool, err := pgxpool.New(ctx, cfg.DatabaseURL)
	if err != nil {
		return err
	}

	defer pool.Close()

	gw, err := gatewayclient.NewClient(cfg.GatewayTarget, cfg.GatewayTLS, logger)
	if err != nil {
		return err
	}

	defer gw.Close()

	handler := webhookingest.NewHandler(gw, webhookingest.NewStore(pool), logger)

	srv := &http.Server{
		Addr:              cfg.Addr,
		Handler:           otelhttp.NewHandler(webhookingest.NewRouter(handler, logger), "webhook-ingest"),
		ReadHeaderTimeout: 5 * time.Second,
	}

	go func() {
		logger.Info("webhook-ingest listening", "addr", cfg.Addr, "env", cfg.Env)
		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			logger.Error("server error", "err", err)
			os.Exit(1)
		}
	}()

	stop := make(chan os.Signal, 1)
	signal.Notify(stop, syscall.SIGINT, syscall.SIGTERM)
	<-stop
	logger.Info("shutting down")

	shutdownCtx , cancel := context.WithTimeout(context.Background() , 10 * time.Second)
	defer cancel()

	return srv.Shutdown(shutdownCtx)
}
