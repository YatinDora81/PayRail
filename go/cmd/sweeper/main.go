package main

import (
	"context"
	"flag"
	"log/slog"
	"os"
	"os/signal"
	"syscall"

	"github.com/payrail/go/internal/budget"
	"github.com/payrail/go/internal/config"
	"github.com/payrail/go/internal/ops"
	"github.com/payrail/go/internal/store"
	"github.com/payrail/go/internal/sweeper"
	"github.com/payrail/go/internal/telemetry"
)

func main() {
	base := slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelInfo})
	logger := slog.New(telemetry.NewSlogHandler(base))
	slog.SetDefault(logger)
	if err := run(logger); err != nil {
		logger.Error("sweeper failed", "err", err)
		os.Exit(1)
	}
}

func run(logger *slog.Logger) error {

	once := flag.Bool("once", false, "run one sweep pass and exit (CronJob mode)")
	flag.Parse()

	cfg, err := config.LoadSweeper()
	if err != nil {
		return err
	}

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	shutdown, err := telemetry.Init(ctx, "sweeper")
	if err != nil {
		return err
	}
	defer func() { _ = shutdown(context.Background()) }()

	db, err := store.New(ctx, cfg.DatabaseURL)
	if err != nil {
		return err
	}
	defer db.Close()

	go func() {
		if err := ops.Serve(ctx, db.Ping, logger); err != nil {
			logger.Error("ops server exited", "err", err)
		}
	}()

	bg, err := budget.New(ctx, cfg.RedisURL)
	if err != nil {
		return err
	}
	defer bg.Close()

	svc := sweeper.NewService(db, bg, cfg.BatchSize, cfg.WebhookRetention, cfg.OutboxRetention, logger)
	logger.Info("sweeper running", "once", *once, "interval", cfg.Interval, "batch", cfg.BatchSize, "env", cfg.Env)
	if *once {
		if _, err := svc.SweepOnce(ctx); err != nil {
			return err
		}
		svc.PurgeOnce(ctx)
		return nil
	}

	svc.Run(ctx, cfg.Interval)
	return nil
}
