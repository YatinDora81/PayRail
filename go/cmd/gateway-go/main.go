package main

import (
	"context"
	"log/slog"
	"os"

	"github.com/payrail/go/internal/config"
	"github.com/payrail/go/internal/gateway"
	"github.com/payrail/go/internal/telemetry"
)

func main() {
	base := slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelInfo})
	logger := slog.New(telemetry.NewSlogHandler(base))
	slog.SetDefault(logger)

	if err := run(); err != nil {
		logger.Error("startup failed", "err", err)
		os.Exit(1)
	}
}

func run() error {
	cfg, err := config.LoadGateway()
	if err != nil {
		return err
	}

	ctx := context.Background()
	shutdown, err := telemetry.Init(ctx, "gateway-go")
	if err != nil {
		return err
	}

	defer func() {
		_ = shutdown(context.Background())
	}()

	var providers []gateway.Provider
	if cfg.Razorpay.KeyID != "" {
		providers = append(providers, gateway.NewRazorpay(cfg.Razorpay.KeyID, cfg.Razorpay.KeySecret, cfg.Razorpay.WebhookSecret, cfg.Razorpay.BaseURL))
	}
	return nil
}
