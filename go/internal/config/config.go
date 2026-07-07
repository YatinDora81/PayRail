package config

import (
	"fmt"
	"os"
	"strconv"
	"time"
)

type CheckoutConfig struct {
	Addr            string // ":8080"
	Env             string // "development" | "production"
	DatabaseURL     string
	RedisURL        string
	GatewayTarget   string // gRPC address of gateway-go, e.g. "localhost:8081"
	GatewayTLS      bool   // dial gateway-go over TLS
	OrderTTL        time.Duration
	UserJWTSecret   string
	RateLimitPerMin int // per user+ip request budget
}

func LoadCheckout() (CheckoutConfig, error) {
	c := CheckoutConfig{
		Addr:            env("PORT", ":8080"),
		Env:             env("ENV", "development"),
		DatabaseURL:     os.Getenv("DATABASE_URL"),
		RedisURL:        env("REDIS_URL", "redis://localhost:6379"),
		GatewayTarget:   env("GATEWAY_TARGET", "localhost:8081"),
		GatewayTLS:      envBool("GATEWAY_TLS", false),
		OrderTTL:        envDur("ORDER_TTL", 15*time.Minute),
		UserJWTSecret:   os.Getenv("USER_JWT_SECRET"),
		RateLimitPerMin: envInt("RATE_LIMIT_PER_MIN", 120),
	}

	if c.DatabaseURL == "" {
		return c, fmt.Errorf("DATABASE_URL is required")
	}
	if c.UserJWTSecret == "" {
		return c, fmt.Errorf("USER_JWT_SECRET is required")
	}
	return c, nil
}

func env(key, val string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return val
}

func envBool(key string, def bool) bool {
	v := os.Getenv(key)
	if v == "" {
		return def
	}
	return v == "1" || v == "true" || v == "TRUE" || v == "yes"
}

func envDur(key string, val time.Duration) time.Duration {
	v := os.Getenv(key)
	if v == "" {
		return val
	}
	d, err := time.ParseDuration(v)
	if err != nil {
		return val
	}
	return d
}

func envInt(key string, val int) int {
	v := os.Getenv(key)
	if v == "" {
		return val
	}
	n, err := strconv.Atoi(v)
	if err != nil {
		return val
	}
	return n
}

type GatewayConfig struct {
	Addr     string
	Env      string
	TLSCert  string
	TLSKey   string
	Razorpay Razorpay
	Stripe   Stripe
	Cashfree Cashfree
	PayPal   PayPal
}

type Razorpay struct {
	KeyID         string
	KeySecret     string
	WebhookSecret string
	BaseURL       string
}

type Stripe struct {
	SecretKey      string
	PublishableKey string
	WebhookSecret  string
	BaseURL        string
}

type Cashfree struct {
	AppID         string
	SecretKey     string
	WebhookSecret string
	BaseURL       string
}

type PayPal struct {
	ClientID      string
	Secret        string
	WebhookID string
	BaseURL       string
}

func LoadGateway() (GatewayConfig, error) {
	return GatewayConfig{
		Addr:    env("PORT", ":8081"),
		Env:     env("ENV", "development"),
		TLSCert: os.Getenv("GATEWAY_TLS_CERT"),
		TLSKey:  os.Getenv("GATEWAY_TLS_KEY"),
		Razorpay: Razorpay{
			KeyID:         os.Getenv("RAZORPAY_KEY_ID"),
			KeySecret:     os.Getenv("RAZORPAY_KEY_SECRET"),
			WebhookSecret: os.Getenv("RAZORPAY_WEBHOOK_SECRET"),
			BaseURL:       os.Getenv("RAZORPAY_BASE_URL"),
		},
		Stripe: Stripe{
			SecretKey:      os.Getenv("STRIPE_SECRET_KEY"),
			PublishableKey: os.Getenv("STRIPE_PUBLISHABLE_KEY"),
			WebhookSecret:  os.Getenv("STRIPE_WEBHOOK_SECRET"),
			BaseURL:        os.Getenv("STRIPE_BASE_URL"),
		},
		Cashfree: Cashfree{
			AppID:         os.Getenv("CASHFREE_APP_ID"),
			SecretKey:     os.Getenv("CASHFREE_SECRET_KEY"),
			WebhookSecret: os.Getenv("CASHFREE_WEBHOOK_SECRET"),
			BaseURL:       os.Getenv("CASHFREE_BASE_URL"),
		},
		PayPal: PayPal{
			ClientID:  os.Getenv("PAYPAL_CLIENT_ID"),
			Secret:    os.Getenv("PAYPAL_SECRET"),
			WebhookID: os.Getenv("PAYPAL_WEBHOOK_ID"),
			BaseURL:   os.Getenv("PAYPAL_BASE_URL"),
		},
	}, nil
}
