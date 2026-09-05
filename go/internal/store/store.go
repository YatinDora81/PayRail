package store

import (
	"context"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/lucsky/cuid"
	"github.com/payrail/go/internal/events"
	"github.com/payrail/go/internal/telemetry"
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

func (s *Store) InsertDeadLetter(ctx context.Context, source, topic, key string, payload []byte, reason string) error {
	telemetry.Counter("payrail_dlq_parked_total").Add(ctx, 1)
	_, err := s.pool.Exec(ctx, `
		INSERT INTO "DeadLetterEvent" ("id","source","topic","key","payload","reason")
		VALUES ($1,$2,$3,$4,$5,$6)`,
		cuid.New(), source, topic, key, payload, reason)
	return err
}

func (s *Store) AssignInvoice(ctx context.Context, orderID, series, currency string, amountMinor int64) error {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)

	if _, err := tx.Exec(ctx,
		`SELECT pg_advisory_xact_lock(hashtextextended($1, 0))`, orderID); err != nil {
		return err
	}

	var exists bool
	if err := tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM "Invoice" WHERE "orderId" = $1)`, orderID).Scan(&exists); err != nil {
		return err
	}
	if exists {
		return tx.Commit(ctx) // replayed order.paid — already numbered
	}

	var n int
	if err := tx.QueryRow(ctx, `
		INSERT INTO "InvoiceCounter" ("series","next") VALUES ($1, 2)
		ON CONFLICT ("series") DO UPDATE SET "next" = "InvoiceCounter"."next" + 1
		RETURNING "next" - 1`, series).Scan(&n); err != nil {
		return err
	}
	taxMinor := TaxIncludedMinor(currency, amountMinor)
	if _, err := tx.Exec(ctx, `
		INSERT INTO "Invoice" ("id","orderId","series","number","currency","amountMinor","taxBps","taxMinor")
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8)
		-- belt-and-braces under the advisory lock — no double invoice, ever
		ON CONFLICT ("orderId") DO NOTHING`,
		cuid.New(), orderID, series, n, currency, amountMinor,
		gstBpsByCurrency[currency], taxMinor); err != nil {
		return err
	}

	if _, err := tx.Exec(ctx, `
		UPDATE "Order" SET "taxAmountMinor" = $2, "updatedAt" = now()
		WHERE "id" = $1`, orderID, taxMinor); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

func (s *Store) SettleRefund(ctx context.Context, t RefundTarget, gatewayRefundID string, receipt []byte) (clawed int, err error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return 0, err
	}
	defer tx.Rollback(ctx)

	// §5: RefundDone may only apply to a refund still in flight.
	ct, err := tx.Exec(ctx, `
		UPDATE "Refund" SET "status" = 'PROCESSED', "gatewayRefundId" = COALESCE(NULLIF($2,''), "gatewayRefundId"), "updatedAt" = now()
		WHERE "id" = $1 AND "status" IN ('PENDING','PROCESSING')`, t.RefundID, gatewayRefundID)
	if err != nil {
		return 0, err
	}
	if ct.RowsAffected() == 0 {
		var cur string
		if err := tx.QueryRow(ctx, `SELECT "status" FROM "Refund" WHERE "id" = $1`, t.RefundID).Scan(&cur); err != nil {
			return 0, err
		}
		if cur == "PROCESSED" {
			return 0, tx.Commit(ctx) // idempotent replay
		}
		return 0, ErrStaleRefund // FAILED etc. — park, never resurrect
	}

	// maths formula to get rid of fractions mismatch
	var refundedAfter int64
	if err := tx.QueryRow(ctx, `
		SELECT COALESCE(SUM("amountMinor"), 0) FROM "Refund"
		WHERE "orderId" = $1 AND "status" = 'PROCESSED'`, t.OrderID).Scan(&refundedAfter); err != nil {
		return 0, err
	}
	refundedBefore := refundedAfter - t.AmountMinor

	clawTotal := func(x int64) int64 {
		if t.CapturedMinor <= 0 {
			return 0
		}
		return int64(t.CreditsGranted) * x / t.CapturedMinor
	}
	clawed = int(clawTotal(refundedAfter) - clawTotal(refundedBefore))

	if clawed > 0 {
		if _, err := tx.Exec(ctx, `
			WITH ins AS (
				INSERT INTO "CreditsLedger" ("id","userId","delta","reason","referenceType","referenceId")
				VALUES ($1,$2,$3,'REFUND','REFUND',$4)
				ON CONFLICT ("referenceType","referenceId","reason") DO NOTHING  -- dedupe key = (REFUND, refundId, REFUND) → one clawback per refund, ever
				RETURNING "delta"
			), clawback AS (
				-- exactly one row: the freshly inserted delta, or 0 on a dedupe replay
				SELECT COALESCE(SUM("delta"), 0) AS "delta" FROM ins
			)
			UPDATE "User" SET "creditsBalance" = "creditsBalance" + clawback."delta"
			FROM clawback
			WHERE "id" = $2`,
			cuid.New(), t.UserID, -clawed, t.RefundID); err != nil {
			return 0, err
		}
	}

	next := "PARTIALLY_REFUNDED"
	if refundedAfter >= t.CapturedMinor { // ≥ not ==: a goodwill over-refund still lands on REFUNDED
		next = "REFUNDED"
	}
	if _, err := tx.Exec(ctx, `
		UPDATE "Order" SET "status" = $2, "updatedAt" = now()
		WHERE "id" = $1 AND "status" IN ('PAID','PARTIALLY_REFUNDED')`, t.OrderID, next); err != nil {
		return 0, err
	}

	// REFUND_DONE mail rides the outbox with the clawback it announces (§2).
	if _, err := tx.Exec(ctx, `
		INSERT INTO "OutboxEvent" ("id","topic","partitionKey","payload")
		VALUES ($1,$2,$3,$4)`,
		cuid.New(), events.TopicOrderRefunded, t.OrderID, receipt); err != nil {
		return 0, err
	}
	return clawed, tx.Commit(ctx)
}

func (s *Store) MarkRefundFailed(ctx context.Context, refundID string) error {
	tag, err := s.pool.Exec(ctx, `
		UPDATE "Refund" SET "status" = 'FAILED', "updatedAt" = now()
		WHERE "id" = $1 AND "status" IN ('PENDING','PROCESSING')`, refundID)
	if err != nil {
		return err
	}

	if tag.RowsAffected() == 1 {
		telemetry.Counter("payrail_refunds_failed_total").Add(ctx, 1)
	}
	return nil
}

type DisputeTarget struct {
	OrderID        string
	UserID         string
	Status         string // current order status
	CreditsGranted int
}

func (s *Store) FindOrderForDispute(ctx context.Context, gateway, gatewayPaymentID string) (DisputeTarget, error) {
	const q = `
		SELECT o."id", o."userId", o."status", o."creditsGranted"
		FROM "Payment" p JOIN "Order" o ON o."id" = p."orderId"
		WHERE p."gateway" = $1 AND p."gatewayPaymentId" = $2`
	var t DisputeTarget
	err := s.pool.QueryRow(ctx, q, gateway, gatewayPaymentID).Scan(&t.OrderID, &t.UserID, &t.Status, &t.CreditsGranted)
	if errors.Is(err, pgx.ErrNoRows) {
		return DisputeTarget{}, ErrNotFound
	}
	return t, err
}

func (s *Store) SettleDispute(ctx context.Context, t DisputeTarget, gateway, gatewayDisputeID, disputeStatus string, amountMinor int64) error {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)

	if _, err := tx.Exec(ctx, `
		INSERT INTO "Dispute" ("id","orderId","gateway","gatewayDisputeId","amountMinor","status","updatedAt")
		VALUES ($1,$2,$3,$4,$5,$6, now())
		ON CONFLICT ("gatewayDisputeId") DO UPDATE SET "status" = EXCLUDED."status", "updatedAt" = now()`,
		cuid.New(), t.OrderID, gateway, gatewayDisputeID, amountMinor, disputeStatus); err != nil {
		return err
	}

	if _, err := tx.Exec(ctx, `
		UPDATE "Order" SET "status" = 'DISPUTED', "updatedAt" = now()
		WHERE "id" = $1 AND "status" IN ('PAID','PARTIALLY_REFUNDED')`, t.OrderID); err != nil {
		return err
	}

	if disputeStatus == "LOST" {
		var alreadyClawed int64
		if err := tx.QueryRow(ctx, `
			WITH clawback_refs AS (
				-- this dispute itself, plus every refund row of the order
				SELECT 'DISPUTE' AS "referenceType", $2 AS "referenceId"
				UNION ALL
				SELECT 'REFUND', r."id" FROM "Refund" r WHERE r."orderId" = $3
			)
			SELECT COALESCE(SUM(-l."delta"), 0)  -- clawbacks are negative deltas ⇒ negate into a positive total
			FROM "CreditsLedger" l
			JOIN clawback_refs USING ("referenceType", "referenceId")  -- count only ledger rows whose (type,id) pair is in the set above
			WHERE l."userId" = $1
			  AND l."reason" IN ('REFUND','CHARGEBACK')`,
			t.UserID, gatewayDisputeID, t.OrderID).Scan(&alreadyClawed); err != nil {
			return err
		}
		remaining := int64(t.CreditsGranted) - alreadyClawed
		if remaining > 0 {

			var newBalance int64
			if err := tx.QueryRow(ctx, `
				WITH ins AS (
					INSERT INTO "CreditsLedger" ("id","userId","delta","reason","referenceType","referenceId")
					VALUES ($1,$2,$3,'CHARGEBACK','DISPUTE',$4)
					ON CONFLICT ("referenceType","referenceId","reason") DO NOTHING  -- dedupe key = (DISPUTE, disputeId, CHARGEBACK) → replay-safe
					RETURNING "delta"
				), clawback AS (
					-- exactly one row: the freshly inserted delta, or 0 on a dedupe replay
					SELECT COALESCE(SUM("delta"), 0) AS "delta" FROM ins
				)
				UPDATE "User"
				   -- cache floors at 0; the ledger keeps the true negative (checked below)
				   SET "creditsBalance" = GREATEST("creditsBalance" + clawback."delta", 0)
				FROM clawback
				WHERE "id" = $2
				RETURNING "creditsBalance"`,
				cuid.New(), t.UserID, -remaining, gatewayDisputeID).Scan(&newBalance); err != nil {
				return err
			}

			if _, err := tx.Exec(ctx, `
				WITH true_balance AS (
					-- the ledger is truth; the cached balance above was clamped at 0
					SELECT COALESCE(SUM("delta"), 0) AS "balance"
					FROM "CreditsLedger" WHERE "userId" = $1
				)
				UPDATE "User"
				   SET "isLocked" = true, "lockedReason" = 'CHARGEBACK_NEGATIVE_BALANCE'
				FROM true_balance tb
				-- lock ONLY when ledger-truth is negative — the clamp above hid it
				WHERE "id" = $1 AND tb."balance" < 0`,
				t.UserID); err != nil {
				return err
			}
		}
	}

	if disputeStatus == "WON" {
		var refunded, captured int64
		if err := tx.QueryRow(ctx, `
			SELECT COALESCE(SUM("amountMinor"),0) FROM "Refund"
			WHERE "orderId" = $1 AND "status" = 'PROCESSED'`, t.OrderID).Scan(&refunded); err != nil {
			return err
		}
		if err := tx.QueryRow(ctx, `
			SELECT COALESCE(SUM("amountMinor"),0) FROM "Payment"
			WHERE "orderId" = $1 AND "status" = 'CAPTURED'`, t.OrderID).Scan(&captured); err != nil {
			return err
		}
		restored := "PAID"
		if refunded > 0 && refunded >= captured {
			restored = "REFUNDED"
		} else if refunded > 0 {
			restored = "PARTIALLY_REFUNDED"
		}
		if _, err := tx.Exec(ctx, `
			UPDATE "Order" SET "status" = $2, "updatedAt" = now()
			WHERE "id" = $1 AND "status" = 'DISPUTED'`, t.OrderID, restored); err != nil {
			return err
		}
	}
	return tx.Commit(ctx)
}

type SettleTarget struct {
	OrderID          string
	UserID           string
	Email            string
	Status           string
	Currency         string
	FinalAmountMinor int64
	CreditsGranted   int
}

func (s *Store) FindOrderForSettlement(ctx context.Context, gateway, gatewayOrderID string) (SettleTarget, error) {
	const q = `
		SELECT o."id", o."userId", u."email", o."status", o."currency", o."finalAmountMinor", o."creditsGranted"
		FROM "Order" o JOIN "User" u ON u."id" = o."userId"
		WHERE o."gateway" = $1 AND o."gatewayOrderId" = $2`
	var t SettleTarget
	err := s.pool.QueryRow(ctx, q, gateway, gatewayOrderID).Scan(
		&t.OrderID, &t.UserID, &t.Email, &t.Status, &t.Currency, &t.FinalAmountMinor, &t.CreditsGranted)
	if errors.Is(err, pgx.ErrNoRows) {
		return SettleTarget{}, ErrNotFound
	}
	return t, err
}

var ErrStaleSettlement = errors.New("stale settlement: order is not in a capturable state")

func (s *Store) SettleOrder(ctx context.Context, t SettleTarget, gateway, gatewayPaymentID string, receipt []byte) error {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)

	ct, err := tx.Exec(ctx, `
		UPDATE "Order" SET "status" = 'PAID', "updatedAt" = now()
		WHERE "id" = $1 AND "status" IN ('CREATED','PENDING_PAYMENT','AUTHORIZED')`, t.OrderID)
	if err != nil {
		return err
	}
	if ct.RowsAffected() == 0 { 
		var cur string
		if err := tx.QueryRow(ctx, `SELECT "status" FROM "Order" WHERE "id" = $1`, t.OrderID).Scan(&cur); err != nil {
			return err
		}
		if cur == "PAID" {
			return tx.Commit(ctx) 
		}
		return ErrStaleSettlement 
	}

	if _, err := tx.Exec(ctx, `
		WITH ins AS (
			INSERT INTO "CreditsLedger" ("id","userId","delta","reason","referenceType","referenceId")
			VALUES ($1,$2,$3,'PURCHASE','ORDER',$4)
			ON CONFLICT ("referenceType","referenceId","reason") DO NOTHING  -- dedupe key = (ORDER, orderId, PURCHASE) → replay inserts NOTHING…
			RETURNING "delta"
		), granted AS (
			-- exactly one row: the freshly inserted delta, or 0 on a dedupe replay
			SELECT COALESCE(SUM("delta"), 0) AS "delta" FROM ins
		)
		UPDATE "User" SET "creditsBalance" = "creditsBalance" + granted."delta"  -- …so delta=0 and this bump is a no-op on replay
		FROM granted
		WHERE "id" = $2`,
		cuid.New(), t.UserID, t.CreditsGranted, t.OrderID); err != nil {
		return err
	}

	if _, err := tx.Exec(ctx, `
		UPDATE "PromotionSpend" SET "status" = 'CONSUMED'
		WHERE "orderId" = $1 AND "status" = 'RESERVED'`, t.OrderID); err != nil {
		return err
	}

	if _, err := tx.Exec(ctx, `
		UPDATE "PromotionUsage" SET "status" = 'CONSUMED'
		WHERE "orderId" = $1 AND "status" = 'RESERVED'`, t.OrderID); err != nil {
		return err
	}

	if gatewayPaymentID != "" {
		if _, err := tx.Exec(ctx, `
			INSERT INTO "Payment" ("id","orderId","gateway","gatewayPaymentId","amountMinor","currency","status","capturedAt","updatedAt")
			VALUES ($1,$2,$3,$4,$5,$6,'CAPTURED', now(), now())
			-- same capture retried ⇒ refresh in place, never a second money row
			ON CONFLICT ("gatewayPaymentId") DO UPDATE SET "status" = 'CAPTURED', "capturedAt" = now(), "updatedAt" = now()`,
			cuid.New(), t.OrderID, gateway, gatewayPaymentID, t.FinalAmountMinor, t.Currency); err != nil {
			return err
		}
	}

	if _, err := tx.Exec(ctx, `
		INSERT INTO "OutboxEvent" ("id","topic","partitionKey","payload")
		VALUES ($1,$2,$3,$4)`,
		cuid.New(), events.TopicOrderPaid, t.OrderID, receipt); err != nil {
		return err
	}

	return tx.Commit(ctx)
}