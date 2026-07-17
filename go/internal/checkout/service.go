package checkout

import (
	"context"
	"errors"
	"log/slog"
	"time"

	"github.com/payrail/go/internal/budget"
	"github.com/payrail/go/internal/gatewayclient"
	"github.com/payrail/go/internal/httpx"
	"github.com/payrail/go/internal/store"
)

type Service struct {
	db       *store.Store
	budget   *budget.Gate
	gateway  *gatewayclient.Client
	orderTTL time.Duration
	logger   *slog.Logger
}

func NewService(db *store.Store, b *budget.Gate, gw *gatewayclient.Client, ttl time.Duration, logger *slog.Logger) *Service {
	return &Service{db: db, budget: b, gateway: gw, orderTTL: ttl, logger: logger}
}

func (s *Service) ListPlans(ctx context.Context, country string) ([]store.PlanForList, error) {
	return s.db.ListPlans(ctx, country)
}

func (s *Service) BankOffers(ctx context.Context, country string) ([]store.BankOfferForList, error) {
	return s.db.ListBankOffers(ctx, country)
}

type CreateOrderInput struct {
	UserID         string
	IdempotencyKey string
	PlanID         string
	Country        string
	City           string
	Gateway        string
	PromotionID    string // optional
	TraceID        string
}

type Quote struct {
	Currency         string
	BaseMinor        int64
	DiscountMinor    int64
	FinalMinor       int64
	TaxIncludedMinor int64 // GST share inside FinalMinor (inclusive pricing, §7)
	Credits          int
}

func (s *Service) PreviewOrder(ctx context.Context, in CreateOrderInput) (Quote, error) {
	pricing, err := s.db.ResolvePricing(ctx, in.PlanID, in.Country, in.City)
	if errors.Is(err, store.ErrNotFound) {
		return Quote{}, httpx.NotFound("no price for this plan in your region")
	}

	if err != nil {
		s.logger.Error("resolve pricing (preview)", "err", err, "traceId", in.TraceID)
		return Quote{}, httpx.Internal()
	}

	discount, credits := int64(0), pricing.Credits
	if in.PromotionID != "" {
		promo, perr := s.db.GetCheckoutPromotion(ctx, in.PromotionID, pricing.Currency)

		if perr != nil && !errors.Is(perr, store.ErrNotFound) {
			s.logger.Error("load promotion (preview)", "err", perr, "traceId", in.TraceID)
			return Quote{}, httpx.Internal()
		}

		if perr == nil {
			d, bonus := computeDiscount(promo, pricing.BaseAmountMinor)
			discount = clampDiscount(d, pricing.BaseAmountMinor, pricing.MaxDiscountBps)
			credits += bonus
		}
	}

	final := pricing.BaseAmountMinor - discount
	if final < 0 {
		final = 0
	}

	return Quote{
		Currency:  pricing.Currency,
		BaseMinor: pricing.BaseAmountMinor,
		DiscountMinor: discount,
		FinalMinor: final,
		Credits: credits,
		TaxIncludedMinor: store.TaxIncludedMinor(pricing.Currency , pricing.BaseAmountMinor),
	}, nil
}

func computeDiscount(p store.CheckoutPromotion, base int64) (discount int64, bonus int) {
	switch p.EffectType {
	case "PERCENT_BPS":
		// base * bps / 10000 (10000 bps = 100%). Safe for realistic amounts.
		return base * int64(p.ValueBps) / 10000, 0
	case "FLAT_AMOUNT":
		if p.AmountMinor > base {
			return base, 0
		}
		return p.AmountMinor, 0
	case "BONUS_CREDITS":
		return 0, p.BonusCredits
	default:
		return 0, 0
	}
}

func clampDiscount(discount, base int64, maxDiscountBps int) int64 {
	maxAllowed := base * int64(maxDiscountBps) / 10000
	if discount > maxAllowed {
		discount = maxAllowed
	}
	if discount > base {
		discount = base
	}
	if discount < 0 {
		discount = 0
	}
	return discount
}
