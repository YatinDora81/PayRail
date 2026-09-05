package main

import (
	"context"
	"flag"
	"log/slog"
	"os"
	"os/signal"
	"sync"
	"syscall"

	"github.com/payrail/go/internal/budget"
	"github.com/payrail/go/internal/config"
	"github.com/payrail/go/internal/events"
	"github.com/payrail/go/internal/kafka"
	"github.com/payrail/go/internal/ops"
	"github.com/payrail/go/internal/reconciler"
	"github.com/payrail/go/internal/store"
	"github.com/payrail/go/internal/telemetry"
)

func main() {
	base := slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelInfo})
	logger := slog.New(telemetry.NewSlogHandler(base))
	slog.SetDefault(logger)
	if err := run(logger); err != nil {
		logger.Error("reconciler failed", "err", err)
		os.Exit(1)
	}
}

func run(logger *slog.Logger) error {

	once := flag.Bool("once", false, "run one reconcile pass and exit (CronJob mode)")
	flag.Parse()

	cfg, err := config.LoadReconciler()
	if err != nil {
		return err
	}

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	shutdown, err := telemetry.Init(ctx, "reconciler")
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

	svc := reconciler.NewService(db, bg, logger)
	if *once {
		logger.Info("reconciler running once", "env", cfg.Env)
		return svc.ReconcileOnce(ctx)
	}

	budgetReader := kafka.NewReader(cfg.Brokers, cfg.GroupID, events.TopicPromotionBudgetUpserted)
	defer budgetReader.Close()
	activatedReader := kafka.NewReader(cfg.Brokers, cfg.GroupID, events.TopicPromotionActivated)
	defer activatedReader.Close()

	logger.Info("reconciler running", "interval", cfg.Interval, "group", cfg.GroupID, "env", cfg.Env)

	var wg sync.WaitGroup
	go func() { defer wg.Done(); _ = budgetReader.Run(ctx, logger, svc.HandleBudgetUpserted) }()
	go func() { defer wg.Done(); _ = activatedReader.Run(ctx, logger, svc.HandleActivated) }()
	go func() { defer wg.Done(); svc.RunPeriodic(ctx, cfg.Interval) }()
	wg.Wait()

	return nil
}
