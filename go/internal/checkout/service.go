package checkout

import (
	"context"
	"log/slog"
	"time"

	"github.com/payrail/go/internal/budget"
	"github.com/payrail/go/internal/gatewayclient"
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
