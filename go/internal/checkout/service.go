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

	discounts , credits := int64(0) , pricing.Credits
	if in.PromotionID!=""{

	}


	return nil, nil
}

