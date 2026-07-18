package checkout

import (
	"context"
	"errors"
	"log/slog"
	"sort"
	"strconv"
	"time"

	"github.com/payrail/go/internal/budget"
	"github.com/payrail/go/internal/domain"
	"github.com/payrail/go/internal/gatewayclient"
	"github.com/payrail/go/internal/httpx"
	"github.com/payrail/go/internal/store"
)

var allowedGateways = map[string]bool{"RAZORPAY": true, "STRIPE": true, "CASHFREE": true, "PAYPAL": true}

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
	Order          store.Order
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

func (s *Service) CreateOrder(ctx context.Context, in CreateOrderInput) (CreateResult, error) {
	if !allowedGateways[in.Gateway] {
		return CreateResult{}, httpx.BadRequest("unsupported gateway")
	}

	locked, err := s.db.UserLocked(ctx, in.UserID)
	if err != nil {
		s.logger.Error("lockout check", "err", err, "traceId", in.TraceID)
		return CreateResult{}, httpx.Internal()
	}

	if locked {
		return CreateResult{}, httpx.NewError(403, "account_locked",
			"this account is locked; contact support")
	}

	claimed, err := s.db.ClaimIdempotency(ctx, in.UserID, "checkout", in.IdempotencyKey)
	if err != nil {
		s.logger.Error("claim idempotency", "err", err, "traceId", in.TraceID)
		return CreateResult{}, httpx.Internal()
	}

	if !claimed {
		existing, gerr := s.db.FindOrderByIdempotency(ctx, in.UserID, in.IdempotencyKey)
		if gerr == nil {
			return CreateResult{Order: existing, Replayed: true}, nil
		}
		if errors.Is(gerr, store.ErrNotFound) {
			return CreateResult{}, httpx.NewError(409, "request_in_progress",
				"a request with this Idempotency-Key is still being processed")
		}
		return CreateResult{}, httpx.Internal()
	}

	succeeded := false

	defer func() {
		status := "FAILED"
		if succeeded {
			status = "DONE"
		}

		mctx := context.WithoutCancel(ctx)
		if merr := s.db.MarkIdempotency(mctx, in.UserID, "checkout", in.IdempotencyKey, status); merr != nil {
			s.logger.Error("mark idempotency", "err", merr, "traceId", in.TraceID)
		}
	}()

	// server price validation
	pricing, err := s.db.ResolvePricing(ctx, in.PlanID, in.Country, in.City)
	if errors.Is(err, store.ErrNotFound) {
		return CreateResult{}, httpx.NotFound("no price for this plan in your region")
	}
	if err != nil {
		s.logger.Error("resolve pricing", "err", err, "traceId", in.TraceID)
		return CreateResult{}, httpx.Internal()
	}

	base := pricing.BaseAmountMinor
	discount := int64(0)
	credits := pricing.Credits
	var reservations []store.Reservation

	if len(in.PromotionIDs) > 0 || in.CouponCode != "" {
		var perr error
		reservations, discount, credits, perr = s.applyPromotions(ctx, in, pricing)
		if perr != nil {
			return CreateResult{}, perr
		}
	}

	final := base - discount
	if final < 0 {
		final = 0
	}

	order, err := s.db.CreateOrder(ctx, store.NewOrder{
		IdempotencyKey:      in.IdempotencyKey,
		UserID:              in.UserID,
		PlanID:              pricing.PlanID,
		Currency:            pricing.Currency,
		BaseAmountMinor:     base,
		DiscountAmountMinor: discount,
		TaxAmountMinor:      0, // GST is computed at settlement/invoice time
		FinalAmountMinor:    final,
		CreditsGranted:      credits,
		ExpiresAt:           time.Now().Add(s.orderTTL),
		TraceID:             in.TraceID,
		Reservations:        reservations,
	})

	if errors.Is(err, store.ErrDuplicate) {
		s.releaseHeld(ctx, reservations, pricing.Currency)
		existing, gerr := s.db.FindOrderByIdempotency(ctx, in.UserID, in.IdempotencyKey)
		if gerr != nil {
			return CreateResult{}, httpx.Internal()
		}
		succeeded = true
		return CreateResult{Order: existing, Replayed: true}, nil
	}

	if errors.Is(err, store.ErrRedemptionLimit) {
		s.releaseHeld(ctx, reservations, pricing.Currency)
		return CreateResult{}, httpx.ConflictWith("promo_exhausted",
			"this promotion has reached its redemption limit; confirm to purchase at full price",
			requoteOf(pricing))
	}

	if err != nil {
		s.releaseHeld(ctx, reservations, pricing.Currency)
		s.logger.Error("create order", "err", err, "traceId", in.TraceID)
		return CreateResult{}, httpx.Internal()
	}

	gwResp, err := s.gateway.CreateOrder(ctx, gatewayclient.CreateOrderRequest{
		Gateway:     in.Gateway,
		OrderID:     order.ID,
		AmountMinor: final,
		Currency:    pricing.Currency,
	})

	if err != nil {
		s.logger.Error("gateway create order", "err", err, "orderId", order.ID, "traceId", in.TraceID)

		if ferr := s.db.FailOrderAndRelease(ctx, order.ID, reservations, pricing.Currency); ferr != nil {
			s.logger.Error("fail+release after gateway error (sweeper will release)", "err", ferr, "orderId", order.ID)
		} else {
			s.releaseHeld(ctx, reservations, pricing.Currency)
		}
		return CreateResult{}, httpx.NewError(502, "gateway_error", "could not start payment, please retry")
	}

	if err := s.db.StampGateway(ctx, order.ID, in.Gateway, gwResp.GatewayOrderID); err != nil {
		s.logger.Error("stamp gateway", "err", err, "orderId", order.ID, "traceId", in.TraceID)
		return CreateResult{}, httpx.Internal()
	}

	gw, gid := in.Gateway, gwResp.GatewayOrderID
	order.Status, order.Gateway, order.GatewayOrderID = "PENDING_PAYMENT", &gw, &gid

	succeeded = true

	return CreateResult{
		Order:          order,
		Gateway:        in.Gateway,
		GatewayOrderID: gwResp.GatewayOrderID,
		ClientParams:   gwResp.ClientParams,
	}, nil
}

func (s *Service) releaseHeld(ctx context.Context, rs []store.Reservation, currency string) {
	var items []budget.Item
	for _, r := range rs {
		if r.AmountMinor > 0 {
			items = append(items, budget.Item{PromoID: r.PromotionID, AmountMinor: r.AmountMinor})
		}
	}
	if len(items) > 0 {
		_ = s.budget.ReleaseN(ctx, currency, items)
	}
}

func requoteOf(p store.Pricing) map[string]any {
	return map[string]any{
		"fullAmount": strconv.FormatInt(p.BaseAmountMinor, 10),
		"currency":   p.Currency,
		"credits":    p.Credits,
	}
}

func (s *Service) applyPromotions(ctx context.Context, in CreateOrderInput, p store.Pricing) ([]store.Reservation, int64, int, error) {
	requote := requoteOf(p)

	ids := in.PromotionIDs
	var typed *store.CheckoutCoupon

	if in.CouponCode != "" {
		c, cerr := s.db.ResolveCouponCode(ctx, in.CouponCode)
		if errors.Is(cerr, store.ErrNotFound) {
			return nil, 0, 0, httpx.ConflictWith("promo_invalid",
				"this coupon code is not valid or has expired; confirm to purchase at full price", requote)
		}
		if cerr != nil {
			s.logger.Error("resolve coupon code", "err", cerr, "traceId", in.TraceID)
			return nil, 0, 0, httpx.Internal()
		}
		typed = &c

		found := false
		for _, id := range ids {
			if id == c.PromotionID {
				found = true
				break
			}
		}

		if !found {
			ids = append(append([]string{}, ids...), c.PromotionID)
		}

		if len(ids) > 3 {
			return nil, 0, 0, httpx.BadRequest("at most 3 promotions per order, including the coupon's")
		}
	}

	promos, err := s.db.GetCheckoutPromotions(ctx, ids, p.Currency)
	if errors.Is(err, store.ErrNotFound) {
		return nil, 0, 0, httpx.ConflictWith("promo_invalid",
			"this promotion is not valid for this plan or has ended; confirm to purchase at full price", requote)
	}
	if err != nil {
		s.logger.Error("load promotions", "err", err, "traceId", in.TraceID)
		return nil, 0, 0, httpx.Internal()
	}

	rules, err := s.db.GetPromotionRules(ctx, ids)
	if err != nil {
		s.logger.Error("load promotion rules", "err", err, "traceId", in.TraceID)
		return nil, 0, 0, httpx.ConflictWith("promo_unavailable",
			"the promotion could not be applied right now; confirm to purchase at full price", requote)
	}

	rulesByPromo := map[string][]domain.Rule{}
	for _, r := range rules {
		rulesByPromo[r.PromotionID] = append(rulesByPromo[r.PromotionID],
			domain.Rule{RuleType: r.RuleType, Config: r.Config})
	}
	rc := domain.RuleContext{PlanID: p.PlanID, Currency: p.Currency, BaseAmountMinor: p.BaseAmountMinor}

	byID := map[string]store.CheckoutPromotion{}
	cands := make([]domain.Candidate, 0, len(promos))

	for _, promo := range promos {

		if typed != nil && typed.PromotionID == promo.ID {
			promo.CouponID = &typed.ID
			promo.PerUserLimit = typed.PerUserLimit
			promo.MaxRedemptions = typed.MaxRedemptions
		}
		byID[promo.ID] = promo

		if promo.HasAnyBudget && !promo.HasBudgetForCurrency {
			return nil, 0, 0, httpx.ConflictWith("promo_invalid",
				"this promotion is not available in your currency; confirm to purchase at full price", requote)
		}

		if len(promo.Effects) == 0 {
			return nil, 0, 0, httpx.ConflictWith("promo_invalid",
				"this promotion is not available in your currency; confirm to purchase at full price", requote)
		}

		if !domain.RulesAllow(rulesByPromo[promo.ID], rc) {
			return nil, 0, 0, httpx.ConflictWith("promo_invalid",
				"this promotion is not valid for this plan or has ended; confirm to purchase at full price", requote)
		}

		if promo.PerUserLimit > 0 || promo.MaxRedemptions != nil {
			perUser, global, cerr := s.db.CountPromotionUsage(ctx, promo.ID, promo.CouponID, in.UserID)
			if cerr != nil {
				s.logger.Error("count promotion usage", "err", cerr, "traceId", in.TraceID)
				return nil, 0, 0, httpx.ConflictWith("promo_unavailable",
					"the promotion could not be applied right now; confirm to purchase at full price", requote)
			}
			if promo.PerUserLimit > 0 && perUser >= promo.PerUserLimit {
				return nil, 0, 0, httpx.ConflictWith("promo_exhausted",
					"you have already used this promotion the maximum number of times; confirm to purchase at full price", requote)
			}
			if promo.MaxRedemptions != nil && global >= *promo.MaxRedemptions {
				return nil, 0, 0, httpx.ConflictWith("promo_exhausted",
					"this promotion has reached its redemption limit; confirm to purchase at full price", requote)
			}
		}

		cands = append(cands, domain.Candidate{
			ID:           promo.ID,
			StackingMode: promo.StackingMode,
			Priority:     promo.Priority,
			CreatedAt:    promo.CreatedAt,
			Effects:      toDomainEffects(promo.Effects),
		})
	}

	res, rerr := domain.Resolve(cands, p.BaseAmountMinor, p.MaxDiscountBps)
	if errors.Is(rerr, domain.ErrNotStackable) {
		return nil, 0, 0, httpx.ConflictWith("promo_not_stackable",
			"these promotions cannot be combined; confirm with one of them or at full price", requote)
	}
	if rerr != nil {
		s.logger.Error("resolve promotions", "err", rerr, "traceId", in.TraceID)
		return nil, 0, 0, httpx.Internal()
	}

	var legs []budget.Item
	for _, c := range res.Contributions {
		if c.DiscountMinor > 0 && byID[c.PromotionID].HasBudgetForCurrency { // only ARMED legs touch Redis — unbudgeted promos hold nothing
			legs = append(legs, budget.Item{PromoID: c.PromotionID, AmountMinor: c.DiscountMinor})
		}
	}

	if len(legs) > 0 {
		if err := s.budget.ReserveN(ctx, p.Currency, legs); err != nil {
			switch {
			case errors.Is(err, budget.ErrExhausted):
				return nil, 0, 0, httpx.ConflictWith("promo_exhausted",
					"this promotion's budget is exhausted; confirm to purchase at full price", requote)
			case errors.Is(err, budget.ErrNotSeeded):
				return nil, 0, 0, httpx.ConflictWith("promo_not_armed",
					"this promotion is not live yet; confirm to purchase at full price", requote)
			default:
				s.logger.Error("budget reserve", "err", err, "traceId", in.TraceID)
				return nil, 0, 0, httpx.ConflictWith("promo_unavailable",
					"the promotion could not be applied right now; confirm to purchase at full price", requote)
			}
		}
	}

	if res.DiscountMinor == 0 && res.BonusCredits == 0 {
		return nil, 0, p.Credits, nil
	}

	var reservations []store.Reservation

	for _, c := range res.Contributions {
		if c.DiscountMinor == 0 && c.BonusCredits == 0 {
			continue // clamped to zero and grants nothing — no rows, no hold
		}
		promo := byID[c.PromotionID]
		kind := "MIXED"
		if len(promo.Effects) == 1 {
			kind = promo.Effects[0].EffectType
		}
		reservations = append(reservations, store.Reservation{
			PromotionID:    promo.ID,
			AmountMinor:    c.DiscountMinor,
			Kind:           kind,
			DiscountMinor:  c.DiscountMinor,
			CreditsGranted: c.BonusCredits,
			CouponID:       promo.CouponID,
			PerUserLimit:   promo.PerUserLimit,
			MaxRedemptions: promo.MaxRedemptions,
		})
	}

	sort.Slice(reservations, func(a, b int) bool {
		return reservations[a].PromotionID < reservations[b].PromotionID
	})

	return reservations, res.DiscountMinor, p.Credits + res.BonusCredits, nil

}

func (s *Service) ListOrders(ctx context.Context, userID, cursor string, limit int) ([]store.Order, error) {
	if limit <= 0 || limit > 100 {
		limit = 25
	}
	return s.db.ListOrdersByUser(ctx, userID, cursor, limit)
}

func (s *Service) GetOrder(ctx context.Context, id, userID string) (store.Order, error) {
	o, err := s.db.GetOrderForUser(ctx, id, userID)
	if errors.Is(err, store.ErrNotFound) {
		return store.Order{}, httpx.NotFound("order not found")
	}
	return o, err
}

func (s *Service) Credits(ctx context.Context, userID, cursor string, limit int) (store.CreditsSummary, error) {
	if limit <= 0 || limit > 100 {
		limit = 25
	}
	return s.db.CreditsForUser(ctx, userID, cursor, limit)
}

