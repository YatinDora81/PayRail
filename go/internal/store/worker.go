package store

import (
	"context"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/lucsky/cuid"
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
