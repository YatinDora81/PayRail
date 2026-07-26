package main

import (
	"context"
	"log/slog"
	"os"
	"os/signal"
	"syscall"

	"github.com/payrail/go/internal/config"
	"github.com/payrail/go/internal/kafka"
	"github.com/payrail/go/internal/outbox"
	"github.com/payrail/go/internal/store"
	"github.com/payrail/go/internal/telemetry"
)

func main() {
	base := slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelInfo})
	logger := slog.New(telemetry.NewSlogHandler(base))
	slog.SetDefault(logger)
	if err := run(logger); err != nil {
		logger.Error("settlement-worker failed", "err", err)
		os.Exit(1)
	}
}

func run(logger *slog.Logger) error {

	cfg, err := config.LoadSettlement()
	if err != nil {
		return err
	}

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	shutdown, err := telemetry.Init(ctx, "settlement-worker")
	if err != nil {
		return err
	}
	defer func() { _ = shutdown(context.Background()) }()

	db, err := store.New(ctx, cfg.DatabaseURL)
	if err != nil {
		return err
	}
	defer db.Close()

	out := kafka.NewWriter(cfg.Brokers)
	defer out.Close()

	go outbox.NewRelay(db, out, logger).RunElected(ctx)

	return nil
}
