package budget

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/redis/go-redis/v9"
)

var (
	ErrExhausted = errors.New("promotion budget exhausted")
	ErrNotSeeded = errors.New("promotion budget not seeded")
)

type Gate struct {
	rdb *redis.Client
}

type Item struct {
	PromoID     string
	AmountMinor int64
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

var reserveNScript = redis.NewScript(`
for i = 1, #KEYS do
  local v = redis.call('GET', KEYS[i])
  if v == false then return -(2*i - 1) end  -- GET on a missing key is Lua false ⇒ never armed — abort before writing
  if tonumber(v) - tonumber(ARGV[i]) < 0 then return -(2*i) end  -- would go negative ⇒ sold out — still nothing written
end
for i = 1, #KEYS do
  redis.call('DECRBY', KEYS[i], ARGV[i])  -- pass 2 runs only when EVERY key survived pass 1
end
return 0
`)

func key(promoID, currency string) string {
	return fmt.Sprintf("promo:budget:%s:%s", promoID, currency)
}

func (g *Gate) ReserveN(ctx context.Context, currency string, items []Item) error {
	keys := make([]string, len(items))
	argv := make([]interface{}, len(items))

	for i, it := range items {
		keys[i] = key(it.PromoID, currency)
		argv[i] = it.AmountMinor
	}

	res, err := reserveNScript.Run(ctx, g.rdb, keys, argv...).Int64()
	if err != nil {
		return err
	}
	if res == 0 {
		return nil
	}

	i := (int(-res) - 1) / 2 // decode the sentinel back to a key index
	if int(-res)%2 == 1 {    // odd sentinel family = not seeded; even = exhausted
		return fmt.Errorf("promo %s: %w", items[i].PromoID, ErrNotSeeded)
	}
	return fmt.Errorf("promo %s: %w", items[i].PromoID, ErrExhausted)
}

func (g *Gate) ReleaseN(ctx context.Context, currency string, items []Item) error {
	var first error
	for _, it := range items {
		if err := g.rdb.IncrBy(ctx, key(it.PromoID, currency), it.AmountMinor).Err(); err != nil && first == nil {
			first = err
		}
	}
	return first
}

func (g *Gate) Current(ctx context.Context, promoID, currency string) (int64, error) {
	v, err := g.rdb.Get(ctx, key(promoID, currency)).Int64()
	if err == redis.Nil {
		return 0, ErrNotSeeded
	}
	return v, err
}

func (g *Gate) Seed(ctx context.Context, promoID, currency string, remaining int64) error {
	return g.rdb.SetNX(ctx, key(promoID, currency), remaining, 0).Err()
}

func (g *Gate) AdjustBy(ctx context.Context, promoID, currency string, delta int64) error {
	return g.rdb.IncrBy(ctx, key(promoID, currency), delta).Err()
}

func (g *Gate) Release(ctx context.Context, promoID, currency string, amountMinor int64) error {
	return g.ReleaseN(ctx, currency, []Item{{PromoID: promoID, AmountMinor: amountMinor}})
}
