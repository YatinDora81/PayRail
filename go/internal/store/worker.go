package store

import (
	"context"
	"errors"

	"github.com/jackc/pgx/v5"
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
