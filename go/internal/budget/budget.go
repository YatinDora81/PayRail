package budget

import (
	"context"
	"time"

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

var rateScript = redis.NewScript(`
	local n = redis.call('INCR' , KEYS[1])
	if n==1  then redis.call('PEXPIRE' , KEYS[1] , ARGV[1] ) end
	return n
`)

func (g *Gate) AllowRate(ctx context.Context, key string, limit int, window time.Duration) bool {
	n, err := rateScript.Run(ctx, g.rdb, []string{"rl:" + key}, window.Milliseconds()).Int64()
	if err != nil {
		return true
	}
	return n <= int64(limit)
}
