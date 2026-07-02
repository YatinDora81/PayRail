package gatewayclient

import (
	"crypto/tls"
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

	return &Client{conn: conn, rpc: gatewaypb.NewGatewayServiceClient(conn), logger: logger} , nil;
}


func (c *Client)Close()error{
	return c.conn.Close()
}