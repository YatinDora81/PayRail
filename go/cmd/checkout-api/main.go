package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/payrail/go/internal/budget"
	"github.com/payrail/go/internal/checkout"
	"github.com/payrail/go/internal/config"
	"github.com/payrail/go/internal/gatewayclient"
	"github.com/payrail/go/internal/middleware"
	"github.com/payrail/go/internal/store"
	"github.com/payrail/go/internal/telemetry"
	"go.opentelemetry.io/contrib/instrumentation/net/http/otelhttp"
)

func main() {
	base := slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelInfo})
	logger := slog.New(telemetry.NewSlogHandler(base))
	slog.SetDefault(logger)

	if err := run(logger); err != nil {
		logger.Error("startup failed", "err" ,err)
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

	svc := checkout.NewService(db, bg, gw, cfg.OrderTTL, logger)
	handler := checkout.NewHandler(svc, logger)

	fmt.Println(handler)

	srv := &http.Server{
		Addr:    cfg.Addr,
		Handler: otelhttp.NewHandler(checkout.NewRouter(handler, db, bg, cfg.UserJWTSecret, rateAllow(bg, cfg.RateLimitPerMin), logger), "checkout-api"),
	}

	go func() {
		logger.Info("checkout-api listening", "addr", cfg.Addr, "env", cfg.Env)
		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			logger.Error("server error", "err", err)
			os.Exit(1)
		}
	}()

	stop := make(chan os.Signal, 1)
	signal.Notify(stop, syscall.SIGINT, syscall.SIGTERM)
	<-stop
	logger.Info("shutting down")

	shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	return srv.Shutdown(shutdownCtx)
}

func rateAllow(bg *budget.Gate, perMin int) middleware.AllowFunc {
	return func(ctx context.Context, key string) bool {
		if perMin <= 0 {
			return true
		}
		return bg.AllowRate(ctx, key, perMin, time.Minute)
	}
}
