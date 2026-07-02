package main

import (
	"context"
	"log/slog"
	"os"

	"github.com/payrail/go/internal/budget"
	"github.com/payrail/go/internal/config"
	"github.com/payrail/go/internal/gatewayclient"
	"github.com/payrail/go/internal/store"
	"github.com/payrail/go/internal/telemetry"
)

func main() {
	base := slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelInfo})
	logger := slog.New(telemetry.NewSlogHandler(base))
	slog.SetDefault(logger)

	if err := run(logger); err != nil {
		logger.Error("startup failed", err)
		os.Exit(1)
	}

}

func run(logger *slog.Logger) error {
	cfg, err := config.LoadCheckout()
	if err != nil {
		return err
	}

	ctx := context.Background()
	shutdown, err := telemetry.Init(ctx, "checkout-api")
	if err != nil {
		return err
	}

	defer func() {
		_ = shutdown(context.Background())
	}()

	db, err := store.New(ctx, cfg.DatabaseURL)
	if err != nil {
		return err
	}

	defer db.Close()

	bg, err := budget.New(ctx, cfg.RedisURL)
	if err != nil {
		return err
	}

	defer bg.Close()

	gw, err := gatewayclient.NewClient(cfg.GatewayTarget, cfg.GatewayTLS, logger)
	if err != nil {
		return err
	}

	defer gw.Close()

	

	return nil
}
