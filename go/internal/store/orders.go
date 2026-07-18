package store

import (
	"context"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/lucsky/cuid"
)

type Reservation struct {
	PromotionID    string
	AmountMinor    int64
	Kind           string // EffectType: PERCENT_BPS | FLAT_AMOUNT | BONUS_CREDITS
	DiscountMinor  int64
	CreditsGranted int
	CouponID       *string
	PerUserLimit   int  // coupon's per-user cap (0 = unlimited) — enforced IN the order txn
	MaxRedemptions *int // coupon's global cap (nil = unlimited) — enforced IN the order txn
}

type NewOrder struct {
	IdempotencyKey      string
	UserID              string
	PlanID              string
	Currency            string
	BaseAmountMinor     int64
	DiscountAmountMinor int64
	TaxAmountMinor      int64
	FinalAmountMinor    int64
	CreditsGranted      int
	ExpiresAt           time.Time
	TraceID             string
	Reservations        []Reservation
}

type Order struct {
	ID                  string    `json:"id"`
	Status              string    `json:"status"`
	Currency            string    `json:"currency"`
	BaseAmountMinor     int64     `json:"-"`
	DiscountAmountMinor int64     `json:"-"`
	TaxAmountMinor      int64     `json:"-"`
	FinalAmountMinor    int64     `json:"-"`
	CreditsGranted      int       `json:"creditsGranted"`
	Gateway             *string   `json:"gateway,omitempty"`
	GatewayOrderID      *string   `json:"gatewayOrderId,omitempty"`
	ExpiresAt           time.Time `json:"expiresAt"`
	CreatedAt           time.Time `json:"createdAt"`
}

var ErrDuplicate = errors.New("duplicate order")

var ErrRedemptionLimit = errors.New("promotion redemption limit reached")

func (s *Store) CreateOrder(ctx context.Context, o NewOrder) (Order, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return Order{}, err
	}
	defer tx.Rollback(ctx)

	const insertOrder = `
		INSERT INTO "Order" (
			"id","idempotencyKey","userId","planId","status","currency",
			"baseAmountMinor","discountAmountMinor","bankDiscountMinor","taxAmountMinor","finalAmountMinor",
			"creditsGranted","traceId","expiresAt","updatedAt"
		) VALUES ($1,$2,$3,$4,'CREATED',$5,$6,$7,0,$8,$9,$10,$11,$12, now())
		RETURNING "id","status","currency","baseAmountMinor","discountAmountMinor","taxAmountMinor","finalAmountMinor","creditsGranted","expiresAt","createdAt"`

	var ord Order

	err = tx.QueryRow(ctx, insertOrder,
		cuid.New(), o.IdempotencyKey, o.UserID, o.PlanID, o.Currency,
		o.BaseAmountMinor, o.DiscountAmountMinor, o.TaxAmountMinor, o.FinalAmountMinor,
		o.CreditsGranted, o.TraceID, o.ExpiresAt,
	).Scan(&ord.ID, &ord.Status, &ord.Currency, &ord.BaseAmountMinor, &ord.DiscountAmountMinor,
		&ord.TaxAmountMinor, &ord.FinalAmountMinor, &ord.CreditsGranted, &ord.ExpiresAt, &ord.CreatedAt)

	if err != nil {
		if isUniqueViolation(err) {
			return Order{}, ErrDuplicate
		}
		return Order{}, err
	}

	for i := range o.Reservations {

		r := &o.Reservations[i]
		if _, err := tx.Exec(ctx, `
			INSERT INTO "PromotionSpend" ("id","promotionId","currency","amountMinor","status","orderId")
			VALUES ($1,$2,$3,$4,'RESERVED',$5)`,
			cuid.New(), r.PromotionID, o.Currency, r.AmountMinor, ord.ID); err != nil {
			return Order{}, err
		}

		if _, err := tx.Exec(ctx, `
			INSERT INTO "OrderDiscount" ("id","orderId","promotionId","couponId","kind","discountMinor","creditsGranted")
			VALUES ($1,$2,$3,$4,$5,$6,$7)`,
			cuid.New(), ord.ID, r.PromotionID, r.CouponID, r.Kind, r.DiscountMinor, r.CreditsGranted); err != nil {
			return Order{}, err
		}

		if r.PerUserLimit > 0 {
			if _, err := tx.Exec(ctx,
				// lock key "promo-user:<promoId>:<userId>" → serialises ONE (promo, user) pair.
				`SELECT pg_advisory_xact_lock(hashtextextended('promo-user:' || $1 || ':' || $2, 0))`,
				r.PromotionID, o.UserID); err != nil {
				return Order{}, err
			}

			var used int
			if err := tx.QueryRow(ctx, `
				SELECT COUNT(*) FROM "PromotionUsage"
				WHERE "promotionId" = $1 AND "userId" = $2 AND "status" IN ('RESERVED','CONSUMED')
				  AND ($3::text IS NULL OR "couponId" = $3)`,
				r.PromotionID, o.UserID, r.CouponID).Scan(&used); err != nil {
				return Order{}, err
			}

			if used >= r.PerUserLimit {
				return Order{}, ErrRedemptionLimit
			}
		}

		if r.MaxRedemptions != nil {
			if _, err := tx.Exec(ctx,
				`SELECT pg_advisory_xact_lock(hashtextextended('promo-redemptions:' || $1, 0))`,
				r.PromotionID); err != nil {
				return Order{}, err
			}

			var total int
			if err := tx.QueryRow(ctx, `
				SELECT COUNT(*) FROM "PromotionUsage"
				WHERE "promotionId" = $1 AND "status" IN ('RESERVED','CONSUMED')
				  -- same coupon scoping as the per-user count
				  AND ($2::text IS NULL OR "couponId" = $2)`,
				r.PromotionID, r.CouponID).Scan(&total); err != nil {
				return Order{}, err
			}

			if total >= *r.MaxRedemptions {
				return Order{}, ErrRedemptionLimit
			}
		}

		if _, err := tx.Exec(ctx, `
			INSERT INTO "PromotionUsage" ("id","promotionId","couponId","userId","orderId","status")
			VALUES ($1,$2,$3,$4,$5,'RESERVED')
			-- replay of THIS order only — cross-order caps live in the locked checks above
			ON CONFLICT ("promotionId","userId","orderId") DO NOTHING`,
			cuid.New(), r.PromotionID, r.CouponID, o.UserID, ord.ID); err != nil {
			return Order{}, err
		}
	}

	if err := tx.Commit(ctx); err != nil {
		return Order{}, err
	}
	return ord, nil
}

func (s *Store) FindOrderByIdempotency(ctx context.Context, userID, key string) (Order, error) {
	return s.scanOrder(ctx, `
		SELECT "id","status","currency","baseAmountMinor","discountAmountMinor","taxAmountMinor","finalAmountMinor","creditsGranted","gateway","gatewayOrderId","expiresAt","createdAt"
		FROM "Order" WHERE "userId" = $1 AND "idempotencyKey" = $2`, userID, key)
}

func (s *Store) ListOrdersByUser(ctx context.Context, userID, cursor string, limit int) ([]Order, error) {
	const q = `
		SELECT "id","status","currency","baseAmountMinor","discountAmountMinor","taxAmountMinor","finalAmountMinor","creditsGranted","gateway","gatewayOrderId","expiresAt","createdAt"
		FROM "Order"
		WHERE "userId" = $1 AND ($2 = '' OR "id" < $2)  -- '' = first page; else rows strictly older than the cursor id
		ORDER BY "id" DESC
		LIMIT $3`

	rows, err := s.pool.Query(ctx, q, userID, cursor, limit)
	if err != nil {
		return nil, err
	}

	defer rows.Close()

	var out []Order

	for rows.Next() {
		var o Order
		if err := rows.Scan(&o.ID, &o.Status, &o.Currency, &o.BaseAmountMinor, &o.DiscountAmountMinor,
			&o.TaxAmountMinor, &o.FinalAmountMinor, &o.CreditsGranted, &o.Gateway, &o.GatewayOrderID,
			&o.ExpiresAt, &o.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, o)
	}

	return out, rows.Err()
}

func (s *Store) GetOrderForUser(ctx context.Context, id, userID string) (Order, error) {
	return s.scanOrder(ctx, `
		SELECT "id","status","currency","baseAmountMinor","discountAmountMinor","taxAmountMinor","finalAmountMinor","creditsGranted","gateway","gatewayOrderId","expiresAt","createdAt"
		FROM "Order" WHERE "id" = $1 AND "userId" = $2`, id, userID)
}

func (s *Store) StampGateway(ctx context.Context, orderID, gateway, gatewayOrderID string) error {
	_, err := s.pool.Exec(ctx, `
		UPDATE "Order"
		SET "gateway" = $2, "gatewayOrderId" = $3, "status" = 'PENDING_PAYMENT', "updatedAt" = now()
		WHERE "id" = $1`, orderID, gateway, gatewayOrderID)
	return err
}

func (s *Store) scanOrder(ctx context.Context, q string, args ...any) (Order, error) {
	var o Order
	err := s.pool.QueryRow(ctx, q, args...).Scan(
		&o.ID, &o.Status, &o.Currency, &o.BaseAmountMinor, &o.DiscountAmountMinor, &o.TaxAmountMinor,
		&o.FinalAmountMinor, &o.CreditsGranted, &o.Gateway, &o.GatewayOrderID, &o.ExpiresAt, &o.CreatedAt,
	)
	if errors.Is(err, pgx.ErrNoRows) {
		return Order{}, ErrNotFound
	}
	return o, err
}

func (s *Store) FailOrderAndRelease(ctx context.Context, orderID string, rs []Reservation, currency string) error {

	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)

	if _, err := tx.Exec(ctx, `
		UPDATE "Order" SET "status" = 'FAILED', "updatedAt" = now() WHERE "id" = $1`, orderID); err != nil {
		return err
	}

	for i := range rs {
		r := &rs[i]
		if _, err := tx.Exec(ctx, `
			INSERT INTO "PromotionSpend" ("id","promotionId","currency","amountMinor","status","orderId")
			-- offset row: −amountMinor nets the RESERVED hold to zero under SUM()
			VALUES ($1,$2,$3,$4,'RELEASED',$5)`,
			cuid.New(), r.PromotionID, currency, -r.AmountMinor, orderID); err != nil {
			return err
		}
	}

	if len(rs) > 0 {
		if _, err := tx.Exec(ctx, `
			UPDATE "PromotionUsage" SET "status" = 'RELEASED'
			WHERE "orderId" = $1 AND "status" = 'RESERVED'`, orderID); err != nil {
			return err
		}
	}
	return tx.Commit(ctx)
}

func (s *Store) ClaimIdempotency(ctx context.Context, userID, endpoint, key string) (claimed bool, err error) {
	tag, err := s.pool.Exec(ctx, `
		INSERT INTO "IdempotencyRecord"
		  ("id","userId","endpoint","idempotencyKey","status","expiresAt")
		VALUES ($1,$2,$3,$4,'IN_PROGRESS', now() + interval '24 hours')
		ON CONFLICT ("userId","endpoint","idempotencyKey") DO UPDATE  -- THE claim: of N concurrent duplicates, exactly one affects a row
		  SET "status" = 'IN_PROGRESS', "expiresAt" = now() + interval '24 hours'
		  WHERE "IdempotencyRecord"."status" = 'FAILED'  -- dead claims are re-claimable in the SAME statement…
		     -- …so a crashed attempt never bricks the key for 24 h
		     OR "IdempotencyRecord"."expiresAt" < now()`,
		cuid.New(), userID, endpoint, key)
	if err != nil {
		return false, err
	}
	return tag.RowsAffected() == 1, nil
}

func (s *Store) MarkIdempotency(ctx context.Context, userID, endpoint, key, status string) error {
	_, err := s.pool.Exec(ctx, `
		UPDATE "IdempotencyRecord" SET "status" = $4
		WHERE "userId" = $1 AND "endpoint" = $2 AND "idempotencyKey" = $3`,
		userID, endpoint, key, status)
	return err
}

func isUniqueViolation(err error) bool {
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) {
		return pgErr.Code == "23505" // unique_violation
	}
	return false
}
