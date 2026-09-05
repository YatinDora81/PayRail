package sweeper

import (
	"context"
	"log/slog"
	"time"

	"github.com/payrail/go/internal/budget"
	"github.com/payrail/go/internal/store"
)

type Service struct {
	db              *store.Store
	budget          *budget.Gate
	batchSize       int
	retention       time.Duration // WebhookEvents raw-body retention
	outboxRetention time.Duration // published OutboxEvent retention (§2)
	logger          *slog.Logger
}

func NewService(db *store.Store, b *budget.Gate, batchSize int, retention, outboxRetention time.Duration, logger *slog.Logger) *Service {
	if batchSize <= 0 {
		batchSize = 200
	}
	return &Service{db: db, budget: b, batchSize: batchSize, retention: retention, outboxRetention: outboxRetention, logger: logger}
}

func (s *Service) SweepOnce(ctx context.Context) (int, error) {
	ids, err := s.db.ListExpiredOrderIDs(ctx, s.batchSize)
	if err != nil {
		return 0, err
	}

	expired := 0
	for _, id := range ids {
		ok, released, err := s.db.ExpireOrderAndRelease(ctx, id)
		if err != nil {
			s.logger.Error("expire order", "orderId", id, "err", err)
			continue
		}

		if !ok {
			continue
		}

		for _, r := range released {
			if err := s.budget.Release(ctx, r.PromotionID, r.Currency, r.AmountMinor); err != nil {
				s.logger.Error("release budget after expiry", "orderId", id, "promotionId", r.PromotionID, "err", err)
			}
		}
		expired++
	}

	if expired > 0 {
		s.logger.Info("sweep complete", "expired", expired)
	}
	return expired, nil
}

func (s *Service) PurgeOnce(ctx context.Context) {
	if s.retention > 0 {
		n, err := s.db.PurgeWebhookEvidence(ctx, s.retention, s.batchSize)
		if err != nil {
			s.logger.Error("purge webhook evidence", "err", err)
		} else if n > 0 {
			s.logger.Info("purged webhook evidence", "rows", n, "olderThan", s.retention.String())
		}
	}

	if s.outboxRetention > 0 {
		purged, err := s.db.PurgePublishedOutbox(ctx, s.outboxRetention, s.batchSize)
		if err != nil {
			s.logger.Error("purge published outbox", "err", err)
		} else if purged > 0 {
			s.logger.Info("purged published outbox", "rows", purged)
		}
	}
}

func (s *Service) Run(ctx context.Context, interval time.Duration) {
	t := time.NewTicker(interval)
	defer t.Stop()
	for {
		if _, err := s.SweepOnce(ctx); err != nil {
			s.logger.Error("sweep failed", "err", err)
		}
		s.PurgeOnce(ctx)
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
	}
}
