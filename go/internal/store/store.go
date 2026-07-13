package store

import (
	"context"
	"errors"

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


type CheckoutPromotion struct {
	ID             string
	EffectType     string // PERCENT_BPS | FLAT_AMOUNT | BONUS_CREDITS
	ValueBps       int
	AmountMinor    int64
	BonusCredits   int
	HasBudget      bool    // true if a PromotionBudget row exists for this currency
	CouponID       *string // the coupon that scopes redemption limits, if any
	PerUserLimit   int     // max redemptions per user (0 = unlimited)
	MaxRedemptions *int    // global redemption cap (nil = unlimited)
}
func (s *Store)GetCheckoutPromotion(ctx context.Context,promoID , currency string)(GetCheckoutPromotion , error){
	
}