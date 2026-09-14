package main

import (
	"context"
	"flag"
	"log/slog"
	"os"
	"os/signal"
	"syscall"

	"github.com/payrail/go/internal/config"
	gw "github.com/payrail/go/internal/gatewayclient"
	"github.com/payrail/go/internal/gatewayrecon"
	"github.com/payrail/go/internal/ops"
	"github.com/payrail/go/internal/store"
	"github.com/payrail/go/internal/telemetry"
)

func main() {
	base := slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelInfo})
	logger := slog.New(telemetry.NewSlogHandler(base))
	slog.SetDefault(logger)
	if err := run(logger); err != nil {
		logger.Error("gateway-reconciler failed", "err", err)
		os.Exit(1)
	}
}

func run(logger *slog.Logger) error {

	once := flag.Bool("once", false, "run one sweep pass and exit (CronJob mode)")
	flag.Parse()

	cfg, err := config.LoadGatewayReconciler()
	if err != nil {
		return err
	}

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	shutdown, err := telemetry.Init(ctx, "gateway-reconciler")
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

	client, err := gw.NewClient(cfg.GatewayTarget, cfg.GatewayTLS, logger)
	if err != nil {
		return err
	}
	defer client.Close()

	svc := gatewayrecon.NewService(db, client, gatewayrecon.Options{
		StuckOrderGrace:      cfg.StuckOrderGrace,
		OrphanOrderGrace:     cfg.OrphanOrderGrace,
		StuckRefundGrace:     cfg.StuckRefundGrace,
		AutoRefundLimitMinor: cfg.AutoRefundLimitMinor,
		Batch:                cfg.BatchSize,
	}, logger)

	logger.Info("gateway-reconciler running", "once", *once, "interval", cfg.Interval,
		"stuckOrderGrace", cfg.StuckOrderGrace, "orphanOrderGrace", cfg.OrphanOrderGrace, "stuckRefundGrace", cfg.StuckRefundGrace,
		"autoRefundLimitMinor", cfg.AutoRefundLimitMinor, "batch", cfg.BatchSize, "env", cfg.Env)

	if *once {
		return svc.Pass(ctx)
	}
	
	svc.Run(ctx, cfg.Interval)
	return nil

}
