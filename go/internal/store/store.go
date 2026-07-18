package store

import (
	"context"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/lucsky/cuid"
)

type Store struct {
	pool *pgxpool.Pool
}

var ErrNotFound = errors.New("not found")

func New(ctx context.Context, dsn string) (*Store, error) {
	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		return nil, err
	}
	if err := pool.Ping(ctx); err != nil {
		return nil, err
	}

	return &Store{pool: pool}, nil
}

func (s *Store) Close() {
	s.pool.Close()
}

func (s *Store) Ping(ctx context.Context) error {
	return s.pool.Ping(ctx)
}

type PlanForList struct {
	ID          string
	Name        string
	Description string
	Credits     int
	Currency    string
	AmountMinor int64
}

func (s *Store) ListPlans(ctx context.Context, country string) ([]PlanForList, error) {
	const q = `
		select p."id" , p."name", COALESCE(p."description", ''), p."credits", pp."currency", pp."amountMinor"
		from "Plans" p 
		join "PlanPrice" pp on pp."planId" = p."id" AND pp."isActive" = true
		WHERE p."isActive" = true AND pp."city" = ''
		and pp."country" = (
			select case when exists (
				select 1 from "PlanPrice" x where x."country" = $1 and x."city" = '' and x."isActive" = true
			) then $1 else 'US' end
		)
		ORDER BY p."credits" ASC
	`
	rows, err := s.pool.Query(ctx, q, country)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []PlanForList
	for rows.Next() {
		var p PlanForList
		if err := rows.Scan(&p.ID, &p.Name, &p.Description, &p.Credits, &p.Currency, &p.AmountMinor); err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

type BankOfferForList struct {
	ID          string `json:"id"`
	Bank        string `json:"bank"`
	Network     string `json:"network"`
	Description string `json:"description"`
	DiscountBps int    `json:"discountBps"`
}

func (s *Store) ListBankOffers(ctx context.Context, country string) ([]BankOfferForList, error) {
	const q = `
		SELECT "id","bank","network",COALESCE("description",''),"discountBps"
		FROM "BankOffer"
		where "isActive" = true and ("country" = $1 OR "country" = '')
		ORDER BY "discountBps" DESC
	`

	rows, err := s.pool.Query(ctx, q, country)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []BankOfferForList
	for rows.Next() {
		var b BankOfferForList
		if err := rows.Scan(&b.ID, &b.Bank, &b.Network, &b.Description, &b.DiscountBps); err != nil {
			return nil, err
		}
		out = append(out, b)
	}
	return out, rows.Err()

}

type Pricing struct {
	PlanID          string
	Credits         int
	MaxDiscountBps  int
	Currency        string
	BaseAmountMinor int64
}

func (s *Store) ResolvePricing(ctx context.Context, planID, country, city string) (Pricing, error) {
	const q = `
		SELECT p."id", p."credits", p."maxDiscountBps", pp."currency", pp."amountMinor"
		FROM "PlanPrice" pp
		JOIN "Plans" p ON p."id" = pp."planId"
		WHERE pp."planId" = $1 AND pp."isActive" = true AND p."isActive" = true
		  AND pp."country" = $2 AND pp."city" IN ($3, '')
		ORDER BY (pp."city" = $3) DESC
		LIMIT 1
	`
	pr, err := s.queryPricing(ctx, q, planID, country, city)
	if errors.Is(err, ErrNotFound) && country != "US" {
		// fall back to the US default
		return s.queryPricing(ctx, q, planID, "US", "")
	}

	return pr, err
}

func (s *Store) queryPricing(ctx context.Context, q string, args ...any) (Pricing, error) {
	var pr Pricing
	err := s.pool.QueryRow(ctx, q, args).Scan(&pr.PlanID, &pr.Credits, &pr.MaxDiscountBps, &pr.Currency, &pr.BaseAmountMinor)
	if errors.Is(err, pgx.ErrNoRows) {
		return Pricing{}, ErrNotFound
	}
	return pr, err
}

type PromotionEffect struct {
	EffectType   string // PERCENT_BPS | FLAT_AMOUNT | BONUS_CREDITS
	ValueBps     int
	AmountMinor  int64
	BonusCredits int
}

type CheckoutPromotion struct {
	ID                   string
	StackingMode         string // EXCLUSIVE | STACKABLE
	Priority             int
	CreatedAt            time.Time
	Effects              []PromotionEffect
	HasAnyBudget         bool    // ≥1 PromotionBudget row exists for this promo
	HasBudgetForCurrency bool    // …and one exists for the checkout currency
	CouponID             *string // OLDEST active coupon — deterministic (lateral)
	PerUserLimit         int     // max redemptions per user (0 = unlimited)
	MaxRedemptions       *int
}

func (s *Store) GetCheckoutPromotions(ctx context.Context, promoIDs []string, currency string) ([]CheckoutPromotion, error) {
	const query = `
			WITH budget_flags AS (
				SELECT "promotionId",
						true                     AS "hasAnyBudget",
						bool_or("currency" = $2) AS "hasBudgetForCurrency"
				FROM "PromotionBudget"
				WHERE "promotionId" = ANY($1)          -- NEW: only the promos we're pricing
				GROUP BY "promotionId"
				),
			oldest_coupon AS (
				SELECT DISTINCT ON (cc."promotionId")
						cc."promotionId", cc."id", cc."perUserLimit", cc."maxRedemptions"
				FROM "CouponCode" cc
				WHERE cc."promotionId" = ANY($1)       -- NEW: same scope as the outer query
					AND cc."isActive" = true
					AND (cc."startsAt" IS NULL OR cc."startsAt" <= now())
					AND (cc."endsAt"   IS NULL OR cc."endsAt"   >= now())
				ORDER BY cc."promotionId", cc."createdAt" ASC
				)
			SELECT pr."id", pr."stackingMode", pr."priority", pr."createdAt",
					e."effectType",
					COALESCE(e."valueBps", 0), COALESCE(e."amountMinor", 0), COALESCE(e."bonusCredits", 0),
					COALESCE(bf."hasAnyBudget", false),
					COALESCE(bf."hasBudgetForCurrency", false),
					c."id", COALESCE(c."perUserLimit", 0), c."maxRedemptions"
			FROM "Promotions" pr
			LEFT JOIN "PromotionEffects" e ON e."promotionId" = pr."id"
			AND (e."currency" IS NULL OR e."currency" = $2)
			LEFT JOIN budget_flags  bf ON bf."promotionId" = pr."id"
			LEFT JOIN oldest_coupon c  ON c."promotionId"  = pr."id"
			WHERE pr."id" = ANY($1) AND pr."isActive" = true
			AND pr."startsAt" <= now() AND pr."endsAt" >= now()
			ORDER BY pr."id", e."createdAt" ASC
	`

	rows, err := s.pool.Query(ctx, query, promoIDs, currency)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	byID := map[string]*CheckoutPromotion{}
	var order []string

	for rows.Next() {
		var (
			p  CheckoutPromotion
			et *string // NULL when the promo has no effect in this currency
			e  PromotionEffect
		)

		if err := rows.Scan(&p.ID, &p.StackingMode, &p.Priority, &p.CreatedAt,
			&et, &e.ValueBps, &e.AmountMinor, &e.BonusCredits,
			&p.HasAnyBudget, &p.HasBudgetForCurrency,
			&p.CouponID, &p.PerUserLimit, &p.MaxRedemptions,
		); err != nil {
			return nil, err
		}

		cur, ok := byID[p.ID]
		if !ok {
			cp := p
			byID[p.ID] = &cp
			order = append(order, p.ID)
			cur = &cp
		}
		if et != nil {
			e.EffectType = *et
			cur.Effects = append(cur.Effects, e)
		}
	}

	if err := rows.Err(); err != nil {
		return nil, err
	}

	if len(order) != len(promoIDs) { // handler dedupes, so counts must match
		return nil, ErrNotFound // ≥1 id missing / inactive / out of window
	}

	out := make([]CheckoutPromotion, 0, len(order))
	for _, id := range order {
		out = append(out, *byID[id])
	}
	return out, nil
}

var gstBpsByCurrency = map[string]int64{
	"INR": 1800, // 18% GST — the series §7's gapless numbering exists for
	// USD/EUR/GBP/AED/SGD default to 0 until finance configures the market.
}

func TaxIncludedMinor(currency string, amountMinor int64) int64 {
	bps := gstBpsByCurrency[currency]
	if bps <= 0 || amountMinor <= 0 {
		return 0
	}
	return amountMinor * bps / (10000 + bps)
}

type CheckoutCoupon struct {
	ID             string
	PromotionID    string
	PerUserLimit   int // 0 = unlimited
	MaxRedemptions *int
}

func (s *Store) ResolveCouponCode(ctx context.Context, code string) (CheckoutCoupon, error) {
	const q = `
		SELECT cc."id", cc."promotionId", COALESCE(cc."perUserLimit", 0), cc."maxRedemptions"
		FROM "CouponCode" cc
		WHERE cc."code" = upper($1) AND cc."isActive" = true
		  AND (cc."startsAt" IS NULL OR cc."startsAt" <= now())
		  AND (cc."endsAt"   IS NULL OR cc."endsAt"   >= now())`
	var c CheckoutCoupon
	err := s.pool.QueryRow(ctx, q, code).Scan(
		&c.ID, &c.PromotionID, &c.PerUserLimit, &c.MaxRedemptions,
	)

	if errors.Is(err, pgx.ErrNoRows) {
		return CheckoutCoupon{}, ErrNotFound
	}
	return c, err
}

type PromotionRule struct {
	PromotionID string
	RuleType    string
	Config      []byte
}

func (s *Store) GetPromotionRules(ctx context.Context, promoIDs []string) ([]PromotionRule, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT "promotionId", "ruleType", "config"
		FROM "PromotionRules" WHERE "promotionId" = ANY($1)`, promoIDs)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []PromotionRule
	for rows.Next() {
		var r PromotionRule
		if err := rows.Scan(&r.PromotionID, &r.RuleType, &r.Config); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
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

func (s *Store) UserLocked(ctx context.Context, userID string) (bool, error) {
	var locked bool
	err := s.pool.QueryRow(ctx,
		`SELECT "isLocked" FROM "User" WHERE "id" = $1`, userID).Scan(&locked)
	if errors.Is(err, pgx.ErrNoRows) {
		return false, nil // an unknown user fails auth elsewhere; not our 403
	}
	return locked, err
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

func (s *Store) FindOrderByIdempotency(ctx context.Context, userID, key string) (Order, error) {
	return s.scanOrder(ctx, `
		SELECT "id","status","currency","baseAmountMinor","discountAmountMinor","taxAmountMinor","finalAmountMinor","creditsGranted","gateway","gatewayOrderId","expiresAt","createdAt"
		FROM "Order" WHERE "userId" = $1 AND "idempotencyKey" = $2`, userID, key)
}

func isUniqueViolation(err error) bool {
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) {
		return pgErr.Code == "23505" // unique_violation
	}
	return false
}

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

func (s *Store) CountPromotionUsage(ctx context.Context, promotionID string, couponID *string, userID string) (perUser int, global int, err error) {
	const q = `
		SELECT
		  COALESCE(SUM(CASE WHEN "userId" = $3 THEN 1 ELSE 0 END), 0),  -- one pass, two counts: per-user via the CASE…
		  COUNT(*)  -- …global via COUNT(*)
		FROM "PromotionUsage"
		WHERE "promotionId" = $1 AND "status" IN ('RESERVED','CONSUMED')
		  AND ($2::text IS NULL OR "couponId" = $2) -- NULL ⇒ promo-wide count; else scoped to THAT coupon (::text lets PG type the NULL)`
	err = s.pool.QueryRow(ctx, q, promotionID, couponID, userID).Scan(&perUser, &global)
	return perUser, global, err
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

func (s *Store) StampGateway(ctx context.Context, orderID, gateway, gatewayOrderID string) error {
	_, err := s.pool.Exec(ctx, `
		UPDATE "Order"
		SET "gateway" = $2, "gatewayOrderId" = $3, "status" = 'PENDING_PAYMENT', "updatedAt" = now()
		WHERE "id" = $1`, orderID, gateway, gatewayOrderID)
	return err
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

type LedgerEntry struct {
	ID            string
	Delta         int
	Reason        string
	ReferenceType string
	ReferenceID   string
	CreatedAt     time.Time
}

// CreditsSummary is balance + one page of history.
type CreditsSummary struct {
	Balance int64
	Ledger  []LedgerEntry
}

func (s *Store) CreditsForUser(ctx context.Context, userID, cursor string, limit int) (CreditsSummary, error) {
	var out CreditsSummary
	if err := s.pool.QueryRow(ctx,
		`SELECT "creditsBalance" FROM "User" WHERE "id" = $1`, userID).Scan(&out.Balance); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return CreditsSummary{}, ErrNotFound
		}
		return CreditsSummary{}, err
	}
	rows, err := s.pool.Query(ctx, `
		SELECT "id","delta","reason","referenceType","referenceId","createdAt"
		FROM "CreditsLedger"
		WHERE "userId" = $1 AND ($2 = '' OR "id" < $2)  -- keyset cursor: '' = first page; else strictly-older ids (cuids sort by time)
		ORDER BY "id" DESC  -- newest first; next cursor = last id of this page
		LIMIT $3`, userID, cursor, limit)
	if err != nil {
		return CreditsSummary{}, err
	}
	defer rows.Close()
	for rows.Next() {
		var e LedgerEntry
		if err := rows.Scan(&e.ID, &e.Delta, &e.Reason, &e.ReferenceType, &e.ReferenceID, &e.CreatedAt); err != nil {
			return CreditsSummary{}, err
		}
		out.Ledger = append(out.Ledger, e)
	}
	return out, rows.Err()
}
