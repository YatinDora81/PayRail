package budget

import (
	"context"

	"github.com/redis/go-redis/v9"
)

type Gate struct {
	rdb *redis.Client
}

func New(ctx context.Context, url string) (*Gate, error) {
	opt, err := redis.ParseURL(url)
	if err != nil {
		return nil, err
	}

	rdb := redis.NewClient(opt)

	if err := rdb.Ping(ctx).Err(); err != nil {
		_ = rdb.Close()
		return nil, err
	}

	return &Gate{rdb: rdb}, nil
}

func (g *Gate) Close() error {
	return g.rdb.Close()
}

func (g *Gate) Ping(ctx context.Context) error {
	return g.rdb.Ping(ctx).Err()
}
