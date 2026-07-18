package checkout

import (
	"context"
	"errors"
	"log/slog"
	"time"

	"github.com/payrail/go/internal/budget"
	"github.com/payrail/go/internal/domain"
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
	PromotionIDs   []string
	CouponCode     string
	TraceID        string
}

type CreateResult struct {
	// order
	Gateway        string
	GatewayOrderID string
	ClientParams   map[string]any
	Replayed       bool
}

type Quote struct {
	Currency         string
	BaseMinor        int64
	DiscountMinor    int64
	FinalMinor       int64
	TaxIncludedMinor int64 // GST share inside FinalMinor (inclusive pricing, §7)
	Credits          int
	PromoApplied     *bool
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
	var promoApplied *bool

	if len(in.PromotionIDs) > 0 || in.CouponCode != "" {
		applied := false
		promoApplied = &applied

		ids := in.PromotionIDs
		if in.CouponCode != "" {
			if c, cerr := s.db.ResolveCouponCode(ctx, in.CouponCode); cerr == nil {
				dup := false
				for _, id := range ids {
					if id == c.PromotionID {
						dup = true
						break
					}
				}
				if !dup && len(ids) < 3 {
					ids = append(append([]string{}, ids...), c.PromotionID)
				}
			}
		}

		promos, perr := s.db.GetCheckoutPromotions(ctx, ids, pricing.Currency)
		if perr != nil && !errors.Is(perr, store.ErrNotFound) {
			s.logger.Error("load promotions (preview)", "err", perr, "traceId", in.TraceID)
			return Quote{}, httpx.Internal()
		}

		if perr == nil {
			rules, rerr := s.db.GetPromotionRules(ctx, ids)
			if rerr != nil {
				s.logger.Error("load promotion rules (preview)", "err", rerr, "traceId", in.TraceID)
				return Quote{}, httpx.Internal()
			}

			rulesByPromo := map[string][]domain.Rule{}
			for _, r := range rules {
				rulesByPromo[r.PromotionID] = append(rulesByPromo[r.PromotionID], domain.Rule{RuleType: r.RuleType, Config: r.Config})
			}

			rc := domain.RuleContext{PlanID: pricing.PlanID, Currency: pricing.Currency, BaseAmountMinor: pricing.BaseAmountMinor}
			var cands []domain.Candidate
			for _, promo := range promos {
				if promo.HasAnyBudget && !promo.HasBudgetForCurrency {
					continue
				}
				if len(promo.Effects) == 0 || !domain.RulesAllow(rulesByPromo[promo.ID], rc) {
					continue
				}
				cands = append(cands, domain.Candidate{ID: promo.ID, StackingMode: promo.StackingMode,
					Priority: promo.Priority, CreatedAt: promo.CreatedAt, Effects: toDomainEffects(promo.Effects)})
			}
			if res, rerr := domain.Resolve(cands, pricing.BaseAmountMinor, pricing.MaxDiscountBps); rerr == nil {
				discount = res.DiscountMinor
				credits += res.BonusCredits
				applied = res.DiscountMinor > 0 || res.BonusCredits > 0
			}
		}
	}

	final := pricing.BaseAmountMinor - discount
	if final < 0 {
		final = 0
	}

	return Quote{
		Currency:         pricing.Currency,
		BaseMinor:        pricing.BaseAmountMinor,
		DiscountMinor:    discount,
		FinalMinor:       final,
		TaxIncludedMinor: store.TaxIncludedMinor(pricing.Currency, final),
		Credits:          credits,
		PromoApplied:     promoApplied,
	}, nil
}


func toDomainEffects(effs []store.PromotionEffect) []domain.Effect {
	out := make([]domain.Effect, len(effs))
	for i, e := range effs {
		out[i] = domain.Effect{EffectType: e.EffectType, ValueBps: e.ValueBps,
			AmountMinor: e.AmountMinor, BonusCredits: e.BonusCredits}
	}
	return out
}

func (s *Service) CreateOrder() {}
