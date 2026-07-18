package gatewayclient

import (
	"context"
	"crypto/tls"
	"fmt"
	"log/slog"

	"github.com/payrail/go/internal/gatewaypb"
	"go.opentelemetry.io/contrib/instrumentation/google.golang.org/grpc/otelgrpc"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/credentials/insecure"
)

type Client struct {
	conn   *grpc.ClientConn
	rpc    gatewaypb.GatewayServiceClient
	logger *slog.Logger
}

func NewClient(target string, tlsEnabled bool, logger *slog.Logger) (*Client, error) {
	creds := insecure.NewCredentials()
	if tlsEnabled {
		creds = credentials.NewTLS(&tls.Config{MinVersion: tls.VersionTLS12})
	}

	conn, err := grpc.NewClient(target, grpc.WithTransportCredentials(creds), grpc.WithStatsHandler(otelgrpc.NewClientHandler()))
	if err != nil {
		return nil, err
	}

	return &Client{conn: conn, rpc: gatewaypb.NewGatewayServiceClient(conn), logger: logger}, nil
}

func (c *Client) Close() error {
	return c.conn.Close()
}

type CreateOrderRequest struct {
	Gateway     string // RAZORPAY | STRIPE | ....
	OrderID     string
	AmountMinor int64
	Currency    string
}

type CreateOrderResponse struct {
	GatewayOrderID string
	ClientParams   map[string]any
}

func (c *Client) CreateOrder(ctx context.Context, req CreateOrderRequest) (CreateOrderResponse, error) {
	resp, err := c.rpc.CreateOrder(ctx, &gatewaypb.CreateOrderRequest{
		Gateway:     gatewaypb.GatewayFromName(req.Gateway),
		OrderId:     req.OrderID,
		AmountMinor: req.AmountMinor, // a real int64 over the wire, no string round-trip
		Currency:    gatewaypb.CurrencyFromName(req.Currency),
	})
	if err != nil {
		return CreateOrderResponse{}, fmt.Errorf("gateway CreateOrder: %w", err)
	}
	return CreateOrderResponse{
		GatewayOrderID: resp.GetGatewayOrderId(),
		ClientParams:   paramsToAny(resp.GetClientParams()),
	}, nil
}


func paramsToAny(in map[string]string) map[string]any {
	if in == nil {
		return nil
	}
	out := make(map[string]any, len(in))
	for k, v := range in {
		out[k] = v
	}
	return out
}
