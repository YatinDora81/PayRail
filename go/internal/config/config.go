package config

import (
	"fmt"
	"os"
	"strconv"
	"time"
)

type CheckoutConfig struct {
	Addr            string        // ":8080"
	Env             string        // "development" | "production"
	DatabaseURL     string        
	RedisURL        string        
	GatewayTarget   string        // gRPC address of gateway-go, e.g. "localhost:8081"
	GatewayTLS      bool          // dial gateway-go over TLS
	OrderTTL        time.Duration 
	UserJWTSecret   string        
	RateLimitPerMin int           // per user+ip request budget 
}

func LoadCheckout() (CheckoutConfig , error){
	c := CheckoutConfig{
		Addr:            env("PORT", ":8080"),
		Env:             env("ENV", "development"),
		DatabaseURL:     os.Getenv("DATABASE_URL"),
		RedisURL:        env("REDIS_URL", "redis://localhost:6379"),
		GatewayTarget:   env("GATEWAY_TARGET", "localhost:8081"),
		GatewayTLS: 	 envBool("GATEWAY_TLS", false),
		OrderTTL:        envDur("ORDER_TTL", 15*time.Minute),
		UserJWTSecret:   os.Getenv("USER_JWT_SECRET"),
		RateLimitPerMin: envInt("RATE_LIMIT_PER_MIN", 120),
	}

	if c.DatabaseURL == ""{
		return c , fmt.Errorf("DATABASE_URL is required")
	}
	if c.UserJWTSecret == "" {
		return c, fmt.Errorf("USER_JWT_SECRET is required")
	}
	return c, nil
}

func env(key , val string) string{
	if v:= os.Getenv(key); v!=""{
		return v
	}
	return val;
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