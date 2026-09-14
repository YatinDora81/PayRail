package store

import (
	"context"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/lucsky/cuid"
	"github.com/payrail/go/internal/telemetry"
)

var ErrStaleRefund = errors.New("stale refund event: refund is not in a settleable state")

type RefundTarget struct {
	RefundID       string
	OrderID        string
	UserID         string
	Email          string
	Status         string
	Currency       string
	AmountMinor    int64 // this refund
	CapturedMinor  int64 // the payment's captured amount
	CreditsGranted int   // what the original purchase granted
}

func (s *Store) FindRefundForSettlement(ctx context.Context, gateway, gatewayRefundID, gatewayPaymentID string, amountMinor int64) (RefundTarget, error) {
	const q = `
		SELECT r."id", r."orderId", o."userId", u."email", r."status", r."currency",
		       r."amountMinor", p."amountMinor", o."creditsGranted"
		FROM "Refund" r
		JOIN "Payment" p ON p."id" = r."paymentId"
		JOIN "Order"   o ON o."id" = r."orderId"
		JOIN "User"    u ON u."id" = o."userId"
		WHERE r."gateway" = $1
		  AND (r."gatewayRefundId" = $2
		       OR ($2 = '' AND p."gatewayPaymentId" = $3 AND r."status" = 'PENDING' AND r."amountMinor" = $4))  -- fallback: id-less webhook matches a PENDING refund of the same amount
		ORDER BY r."createdAt" ASC  -- two identical PENDING refunds ⇒ deterministically pick the oldest
		LIMIT 1`
	var t RefundTarget
	err := s.pool.QueryRow(ctx, q, gateway, gatewayRefundID, gatewayPaymentID, amountMinor).Scan(
		&t.RefundID, &t.OrderID, &t.UserID, &t.Email, &t.Status, &t.Currency,
		&t.AmountMinor, &t.CapturedMinor, &t.CreditsGranted)
	if errors.Is(err, pgx.ErrNoRows) {
		return RefundTarget{}, ErrNotFound
	}
	return t, err
}

type PromotionBudgetRow struct {
	PromotionID    string
	Currency       string
	CapMinor       int64
	RemainingMinor int64
}

const spendTotalsCTE = `WITH spend_totals AS (
		SELECT "promotionId", "currency", SUM("amountMinor") AS "spentMinor"
		FROM "PromotionSpend"
		GROUP BY "promotionId", "currency"
	)`

const budgetRemainingExpr = `b."capMinor" - COALESCE(st."spentMinor", 0)`

func (s *Store) ActiveBudgets(ctx context.Context) ([]PromotionBudgetRow, error) {
	q := spendTotalsCTE + `
	      SELECT b."promotionId", b."currency", b."capMinor", ` + budgetRemainingExpr + `
	      FROM "PromotionBudget" b
	      JOIN "Promotions" p ON p."id" = b."promotionId"
	      LEFT JOIN spend_totals st ON st."promotionId" = b."promotionId" AND st."currency" = b."currency"
	      WHERE p."isActive" = true AND p."startsAt" <= now() AND p."endsAt" >= now()`
	return s.scanBudgetRows(ctx, q)
}

func (s *Store) scanBudgetRows(ctx context.Context, q string, args ...any) ([]PromotionBudgetRow, error) {
	rows, err := s.pool.Query(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []PromotionBudgetRow
	for rows.Next() {
		var r PromotionBudgetRow
		if err := rows.Scan(&r.PromotionID, &r.Currency, &r.CapMinor, &r.RemainingMinor); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

func (s *Store) ClearPendingDrift(ctx context.Context, promotionID, currency string) error {
	_, err := s.pool.Exec(ctx, `
		UPDATE "ReconciliationLog" SET "corrected" = true
		WHERE "kind" = 'BUDGET_DRIFT_PENDING' AND "promotionId" = $1 AND "currency" = $2 AND "corrected" = false`,
		promotionID, currency)
	return err
}

func (s *Store) BudgetRemaining(ctx context.Context, promotionID, currency string) (int64, error) {
	q := spendTotalsCTE + `
	      SELECT ` + budgetRemainingExpr + `
	      FROM "PromotionBudget" b
	      LEFT JOIN spend_totals st ON st."promotionId" = b."promotionId" AND st."currency" = b."currency"
	      WHERE b."promotionId" = $1 AND b."currency" = $2`
	var remaining int64
	err := s.pool.QueryRow(ctx, q, promotionID, currency).Scan(&remaining)
	if errors.Is(err, pgx.ErrNoRows) {
		return 0, ErrNotFound
	}
	return remaining, err
}

func (s *Store) PendingDrift(ctx context.Context, promotionID, currency string) (driftMinor int64, found bool, err error) {
	err = s.pool.QueryRow(ctx, `
		SELECT "driftMinor" FROM "ReconciliationLog"
		WHERE "kind" = 'BUDGET_DRIFT_PENDING' AND "promotionId" = $1 AND "currency" = $2 AND "corrected" = false
		ORDER BY "createdAt" DESC LIMIT 1`, promotionID, currency).Scan(&driftMinor)
	if errors.Is(err, pgx.ErrNoRows) {
		return 0, false, nil
	}
	return driftMinor, err == nil, err
}

func (s *Store) RecordPendingDrift(ctx context.Context, promotionID, currency string, driftMinor int64) error {
	if err := s.ClearPendingDrift(ctx, promotionID, currency); err != nil { // one open row per counter
		return err
	}
	_, err := s.pool.Exec(ctx, `
		INSERT INTO "ReconciliationLog" ("id","kind","promotionId","currency","driftMinor","note")
		VALUES ($1,'BUDGET_DRIFT_PENDING',$2,$3,$4,'awaiting second-tick confirmation')`,
		cuid.New(), promotionID, currency, driftMinor)
	return err
}

func (s *Store) LogDrift(ctx context.Context, promotionID, currency string, driftMinor int64) error {
	_, err := s.pool.Exec(ctx, `
		INSERT INTO "ReconciliationLog" ("id","kind","promotionId","currency","driftMinor","corrected")
		VALUES ($1,'BUDGET_DRIFT',$2,$3,$4,true)`,
		cuid.New(), promotionID, currency, driftMinor)
	if err != nil {
		return err
	}
	return s.ClearPendingDrift(ctx, promotionID, currency)
}

func (s *Store) BudgetsForPromotion(ctx context.Context, promotionID string) ([]PromotionBudgetRow, error) {
	q := spendTotalsCTE + `
	      SELECT b."promotionId", b."currency", b."capMinor", ` + budgetRemainingExpr + `
	      FROM "PromotionBudget" b
	      LEFT JOIN spend_totals st ON st."promotionId" = b."promotionId" AND st."currency" = b."currency"
	      WHERE b."promotionId" = $1`
	return s.scanBudgetRows(ctx, q, promotionID)
}

func (s *Store) ListExpiredOrderIDs(ctx context.Context, limit int) ([]string, error) {
	const q = `
		SELECT "id" FROM "Order"
		WHERE "status" IN ('CREATED','PENDING_PAYMENT') AND "expiresAt" < now()
		ORDER BY "expiresAt" ASC
		LIMIT $1`
	rows, err := s.pool.Query(ctx, q, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var ids []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		ids = append(ids, id)
	}
	return ids, rows.Err()
}

type Released struct {
	PromotionID string
	Currency    string
	AmountMinor int64
}

func (s *Store) ExpireOrderAndRelease(ctx context.Context, orderID string) (bool, []Released, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return false, nil, err
	}
	defer tx.Rollback(ctx)

	ct, err := tx.Exec(ctx, `
		UPDATE "Order" SET "status" = 'EXPIRED', "updatedAt" = now()
		WHERE "id" = $1 AND "status" IN ('CREATED','PENDING_PAYMENT')`, orderID)
	if err != nil {
		return false, nil, err
	}
	if ct.RowsAffected() == 0 {
		return false, nil, tx.Commit(ctx)
	}

	rows, err := tx.Query(ctx, `
		SELECT "promotionId","currency","amountMinor" FROM "PromotionSpend"
		WHERE "orderId" = $1 AND "status" = 'RESERVED'`, orderID)
	if err != nil {
		return false, nil, err
	}

	var released []Released
	for rows.Next() {
		var r Released
		if err := rows.Scan(&r.PromotionID, &r.Currency, &r.AmountMinor); err != nil {
			rows.Close()
			return false, nil, err
		}
		released = append(released, r)
	}

	rows.Close()
	if err := rows.Err(); err != nil {
		return false, nil, err
	}

	for _, r := range released {
		if _, err := tx.Exec(ctx, `
			INSERT INTO "PromotionSpend" ("id","promotionId","currency","amountMinor","status","orderId")
			-- sign flip: the release row stores −amountMinor
			VALUES ($1,$2,$3,$4,'RELEASED',$5)`,
			cuid.New(), r.PromotionID, r.Currency, -r.AmountMinor, orderID); err != nil {
			return false, nil, err
		}
	}

	if _, err := tx.Exec(ctx, `
		UPDATE "PromotionUsage" SET "status" = 'RELEASED'
		WHERE "orderId" = $1 AND "status" = 'RESERVED'`, orderID); err != nil {
		return false, nil, err
	}

	if err := tx.Commit(ctx); err != nil {
		return false, nil, err
	}
	return true, released, nil
}

func (s *Store) PurgeWebhookEvidence(ctx context.Context, olderThan time.Duration, limit int) (int64, error) {
	ct, err := s.pool.Exec(ctx, `
		WITH batch AS (
			SELECT "ctid" FROM "WebhookEvents"  -- ctid = physical row address — cheapest join handle for a batch UPDATE
			WHERE "receivedAt" < now() - $1::interval AND "purgedAt" IS NULL  -- purgedAt is the tombstone: row + bodySha stay, evidence goes
			LIMIT $2
		)
		UPDATE "WebhookEvents" w
		   SET "rawBody" = NULL, "signature" = NULL, "purgedAt" = now()
		FROM batch
		WHERE w."ctid" = batch."ctid"`,
		olderThan, limit)
	if err != nil {
		return 0, err
	}
	return ct.RowsAffected(), nil
}

type StuckOrder struct {
	ID             string
	UserID         string
	Gateway        string
	GatewayOrderID string
	Currency       string
	AmountMinor    int64
}

type UnstampedOrder struct {
	ID      string
	Gateway string
}

type StuckRefund struct {
	ID               string
	OrderID          string
	Gateway          string
	GatewayRefundID  string // "" when the CREATE timed out before the id landed
	GatewayPaymentID string
	GatewayOrderID   string
	IdempotencyKey   string
	AmountMinor      int64
	Currency         string
}

type OrphanCapture struct {
	DeadLetterID     string
	EventID          string
	OrderID          string
	Gateway          string
	GatewayOrderID   string // the provider's order key — Cashfree refunds are created under it
	GatewayPaymentID string
	AmountMinor      int64
	Currency         string
}

const (
	CorrectionOrphanOrderRecovered      = "ORPHAN_ORDER_RECOVERED"
	CorrectionOrderCaptureHealed        = "ORDER_CAPTURE_HEALED"
	CorrectionOrderFailedFromProvider   = "ORDER_FAILED_FROM_PROVIDER"
	CorrectionRefundHealed              = "REFUND_HEALED"
	CorrectionRefundFailedFromProvider  = "REFUND_FAILED_FROM_PROVIDER"
	CorrectionOrphanCaptureAutoRefunded = "ORPHAN_CAPTURE_AUTO_REFUNDED"
	CorrectionOrphanCaptureNeedsReview  = "ORPHAN_CAPTURE_NEEDS_REVIEW"
)

type Correction struct {
	Kind         string
	DeadLetterID string // "" → NULL
	Note         string
	Corrected    bool
}

func (s *Store) OrdersUnstamped(ctx context.Context, olderThan time.Duration, limit int) ([]UnstampedOrder, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT "id", "gateway" FROM "Order"
		WHERE "status" = 'CREATED' AND "gateway" IS NOT NULL AND "gatewayOrderId" IS NULL
		  AND "createdAt" < now() - $1::interval
		ORDER BY "createdAt" ASC
		LIMIT $2`, olderThan, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []UnstampedOrder
	for rows.Next() {
		var o UnstampedOrder
		if err := rows.Scan(&o.ID, &o.Gateway); err != nil {
			return nil, err
		}
		out = append(out, o)
	}
	return out, rows.Err()
}

func (s *Store) LogReconciliation(ctx context.Context, c Correction) error {
	_, err := s.pool.Exec(ctx, `
		INSERT INTO "ReconciliationLog" ("id","kind","deadLetterId","note","corrected")
		VALUES ($1,$2,NULLIF($3,''),$4,$5)`, cuid.New(), c.Kind, c.DeadLetterID, c.Note, c.Corrected)
	return err
}

func (s *Store) EnqueueOutbox(ctx context.Context, topic, partitionKey string, payload []byte) error {
	_, err := s.pool.Exec(ctx, `
		INSERT INTO "OutboxEvent" ("id","topic","partitionKey","payload")
		VALUES ($1,$2,$3,$4)`, cuid.New(), topic, partitionKey, payload)
	return err
}

func (s *Store) OrdersAwaitingSettlement(ctx context.Context, olderThan time.Duration, limit int) ([]StuckOrder, error) {
	const q = `
		SELECT "id","userId","gateway","gatewayOrderId","currency","finalAmountMinor"
		FROM "Order"
		WHERE "status" = 'PENDING_PAYMENT'
		  AND "gatewayOrderId" IS NOT NULL
		  AND "updatedAt" < now() - $1::interval
		ORDER BY "updatedAt" ASC
		LIMIT $2`
	rows, err := s.pool.Query(ctx, q, olderThan, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []StuckOrder
	for rows.Next() {
		var o StuckOrder
		if err := rows.Scan(&o.ID, &o.UserID, &o.Gateway, &o.GatewayOrderID, &o.Currency, &o.AmountMinor); err != nil {
			return nil, err
		}
		out = append(out, o)
	}
	return out, rows.Err()
}

func (s *Store) FailStuckOrder(ctx context.Context, orderID string) error {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)

	ct, err := tx.Exec(ctx, `
		UPDATE "Order" SET "status" = 'FAILED', "updatedAt" = now()
		WHERE "id" = $1 AND "status" IN ('CREATED','PENDING_PAYMENT','AUTHORIZED')`, orderID)
	if err != nil {
		return err
	}
	if ct.RowsAffected() == 0 {
		return tx.Commit(ctx)
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO "PromotionSpend" ("id","promotionId","currency","amountMinor","status","orderId")
		SELECT $2 || '_' || "id", "promotionId", "currency", -"amountMinor", 'RELEASED', "orderId"
		FROM "PromotionSpend"
		WHERE "orderId" = $1 AND "status" = 'RESERVED'`, orderID, cuid.New()); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `
		UPDATE "PromotionUsage" SET "status" = 'RELEASED'
		WHERE "orderId" = $1 AND "status" = 'RESERVED'`, orderID); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

func (s *Store) RefundsAwaitingResolution(ctx context.Context, olderThan time.Duration, limit int) ([]StuckRefund, error) {
	const q = `
		SELECT r."id", r."orderId", r."gateway", COALESCE(r."gatewayRefundId", ''),
		       p."gatewayPaymentId", COALESCE(o."gatewayOrderId", ''),
		       r."idempotencyKey", r."amountMinor", r."currency"
		FROM "Refund" r
		JOIN "Payment" p ON p."id" = r."paymentId"
		JOIN "Order"   o ON o."id" = r."orderId"
		WHERE r."status" IN ('PENDING','PROCESSING') AND r."createdAt" < now() - $1::interval  -- PROCESSING is admin-api's "accepted, not settled" 
		ORDER BY r."createdAt" ASC
		LIMIT $2`
	rows, err := s.pool.Query(ctx, q, olderThan, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []StuckRefund
	for rows.Next() {
		var r StuckRefund
		if err := rows.Scan(&r.ID, &r.OrderID, &r.Gateway, &r.GatewayRefundID,
			&r.GatewayPaymentID, &r.GatewayOrderID, &r.IdempotencyKey, &r.AmountMinor, &r.Currency); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
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

func (s *Store) ParkedCapturesOnTerminalOrders(ctx context.Context, limit int) ([]OrphanCapture, error) {
	const q = `
		SELECT d."id",
		       d."payload"->>'eventId',
		       o."id",
		       d."payload"->>'gateway',
		       o."gatewayOrderId",
		       d."payload"->>'gatewayPaymentId',
		       (d."payload"->>'amountMinor')::bigint,
		       d."payload"->>'currency'
		FROM "DeadLetterEvent" d
		JOIN "Order" o ON o."gatewayOrderId" = d."payload"->>'gatewayOrderId'
		WHERE d."reason" LIKE 'stale capture%'  -- settlement parks as "stale capture: …" 
		  AND d."payload"->>'kind' = 'PAYMENT'
		  AND d."needsReview" = false
		  AND o."status" IN ('EXPIRED','FAILED','CANCELLED')
		  AND NOT EXISTS (SELECT 1 FROM "ReconciliationLog" rl WHERE rl."deadLetterId" = d."id")
		  AND NOT EXISTS (SELECT 1 FROM "Refund" r
		                  WHERE r."idempotencyKey" = 'orphan:' || (d."payload"->>'eventId')
		                    AND r."status" <> 'FAILED')
		ORDER BY d."createdAt" ASC
		LIMIT $1`
	rows, err := s.pool.Query(ctx, q, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []OrphanCapture
	for rows.Next() {
		var o OrphanCapture
		if err := rows.Scan(&o.DeadLetterID, &o.EventID, &o.OrderID, &o.Gateway,
			&o.GatewayOrderID, &o.GatewayPaymentID, &o.AmountMinor, &o.Currency); err != nil {
			return nil, err
		}
		out = append(out, o)
	}
	return out, rows.Err()
}

func (s *Store) MarkNeedsReview(ctx context.Context, deadLetterID string) error {
	_, err := s.pool.Exec(ctx, `
		UPDATE "DeadLetterEvent" SET "needsReview" = true WHERE "id" = $1`, deadLetterID)
	return err
}

func (s *Store) CreateOrphanRefund(ctx context.Context, orderID, gatewayPaymentID, gateway string, amountMinor int64, currency, idempotencyKey string) (string, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return "", err
	}
	defer tx.Rollback(ctx)

	var paymentID string
	if err := tx.QueryRow(ctx, `
		INSERT INTO "Payment" ("id","orderId","gateway","gatewayPaymentId","amountMinor","currency","status","capturedAt","updatedAt")
		VALUES ($1,$2,$3,$4,$5,$6,'CAPTURED', now(), now())
		ON CONFLICT ("gatewayPaymentId") DO UPDATE SET "updatedAt" = now()
		RETURNING "id"`,
		cuid.New(), orderID, gateway, gatewayPaymentID, amountMinor, currency).Scan(&paymentID); err != nil {
		return "", err
	}

	refundID := cuid.New()
	ct, err := tx.Exec(ctx, `
		INSERT INTO "Refund" ("id","orderId","paymentId","gateway","amountMinor","currency","status","reason","idempotencyKey","updatedAt")
		VALUES ($1,$2,$3,$4,$5,$6,'PENDING','orphan capture auto-refund (§1)',$7, now())
		ON CONFLICT ("idempotencyKey") DO NOTHING`,
		refundID, orderID, paymentID, gateway, amountMinor, currency, idempotencyKey)
		
	if err != nil {
		return "", err
	}
	if ct.RowsAffected() == 0 { 
		if err := tx.QueryRow(ctx, `
			SELECT "id" FROM "Refund" WHERE "idempotencyKey" = $1`, idempotencyKey).Scan(&refundID); err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				return "", ErrNotFound
			}
			return "", err
		}
	}
	return refundID, tx.Commit(ctx)
}