package store

import (
	"context"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
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

func (s *Store) UserLocked(ctx context.Context, userID string) (bool, error) {
	var locked bool
	err := s.pool.QueryRow(ctx,
		`SELECT "isLocked" FROM "User" WHERE "id" = $1`, userID).Scan(&locked)
	if errors.Is(err, pgx.ErrNoRows) {
		return false, nil // an unknown user fails auth elsewhere; not our 403
	}
	return locked, err
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
