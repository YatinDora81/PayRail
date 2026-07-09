package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"os"
	"os/signal"
	"syscall"

	"github.com/payrail/go/internal/config"
	"github.com/payrail/go/internal/gateway"
	"github.com/payrail/go/internal/gatewaypb"
	"github.com/payrail/go/internal/telemetry"
	"go.opentelemetry.io/contrib/instrumentation/google.golang.org/grpc/otelgrpc"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/health"
	healthpb "google.golang.org/grpc/health/grpc_health_v1"
	"google.golang.org/grpc/reflection"
)

func main() {
	base := slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelInfo})
	logger := slog.New(telemetry.NewSlogHandler(base))
	slog.SetDefault(logger)

	if err := run(logger); err != nil {
		logger.Error("startup failed", "err", err)
		os.Exit(1)
	}
}

func run(logger *slog.Logger) error {
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
	if cfg.Stripe.SecretKey != "" {
		providers = append(providers, gateway.NewStripe(cfg.Stripe.SecretKey, cfg.Stripe.PublishableKey, cfg.Stripe.WebhookSecret, cfg.Stripe.BaseURL))
	}
	if cfg.Cashfree.AppID != "" {
		providers = append(providers, gateway.NewCashfree(cfg.Cashfree.AppID, cfg.Cashfree.SecretKey, cfg.Cashfree.WebhookSecret, cfg.Cashfree.BaseURL, cfg))
	}
	if cfg.PayPal.ClientID != "" {
		providers = append(providers, gateway.NewPayPal(cfg.PayPal.ClientID, cfg.PayPal.Secret, cfg.PayPal.WebhookID, cfg.PayPal.BaseURL, cfg))
	}

	registry := gateway.NewRegistry(providers...)
	if len(registry.Names()) == 0 {
		return errors.New("no payment providers configured (set at least one provider's credentials)")
	}

	var opts []grpc.ServerOption
	opts = append(opts, grpc.StatsHandler(otelgrpc.NewServerHandler()))
	if cfg.TLSCert != "" && cfg.TLSKey != "" {
		creds, err := credentials.NewServerTLSFromFile(cfg.TLSCert, cfg.TLSKey)
		if err != nil {
			return fmt.Errorf("load tls cert: %w", err)
		}

		opts = append(opts, grpc.Creds(creds))
		logger.Info("gateway-go TLS enabled")
	}

	srv := grpc.NewServer(opts...)
	gatewaypb.RegisterGatewayServiceServer(srv, gateway.NewGRPCServer(registry, logger))

	hs := health.NewServer()
	hs.SetServingStatus("payrail.gateway.v1.GatewayService", healthpb.HealthCheckResponse_SERVING)
	healthpb.RegisterHealthServer(srv, hs)
	reflection.Register(srv)

	lis, err := net.Listen("tcp", cfg.Addr)
	if err != nil {
		return fmt.Errorf("listen %s: %w", cfg.Addr, err)
	}

	go func() {
		logger.Info("gateway-go gRPC listening", "addr", cfg.Addr, "providers", registry.Names(), "env", cfg.Env)
		if err := srv.Serve(lis); err != nil {
			logger.Error("serve error", "err", err)
			os.Exit(1)
		}
	}()

	stop := make(chan os.Signal, 1)
	signal.Notify(stop, syscall.SIGINT, syscall.SIGTERM)
	<-stop
	logger.Info("shutting down")
	srv.GracefulStop()

	return nil
}
