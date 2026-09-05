package reconciler

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"time"

	"github.com/payrail/go/internal/budget"
	"github.com/payrail/go/internal/events"
	"github.com/payrail/go/internal/kafka"
	"github.com/payrail/go/internal/store"
	"github.com/payrail/go/internal/telemetry"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/metric"
)

type Service struct {
	db     *store.Store
	budget *budget.Gate
	logger *slog.Logger
}

func NewService(db *store.Store, b *budget.Gate, logger *slog.Logger) *Service {
	return &Service{db: db, budget: b, logger: logger}
}

func (s *Service) ReconcileOnce(ctx context.Context) error {

	rows, err := s.db.ActiveBudgets(ctx)
	if err != nil {
		return err
	}

	corrected := 0
	for _, r := range rows {
		cur, err := s.budget.Current(ctx, r.PromotionID, r.Currency)
		if errors.Is(err, budget.ErrNotSeeded) {
			if err := s.budget.Seed(ctx, r.PromotionID, r.Currency, r.RemainingMinor); err != nil {
				s.logger.Error("seed missing counter", "promotionId", r.PromotionID, "err", err)
			}
			_ = s.db.ClearPendingDrift(ctx, r.PromotionID, r.Currency)
			continue
		}

		if err != nil {
			s.logger.Error("read counter during reconcile", "promotionId", r.PromotionID, "err", err)
			continue
		}

		remaining, err := s.db.BudgetRemaining(ctx, r.PromotionID, r.Currency)
		if err != nil {
			s.logger.Error("read ledger during reconcile", "promotionId", r.PromotionID, "err", err)
			continue
		}

		telemetry.Gauge("payrail_budget_remaining_minor").Record(ctx, cur, metric.WithAttributes(attribute.String("promotion", r.PromotionID), attribute.String("currency", r.Currency)))
		telemetry.Gauge("payrail_budget_cap_minor").Record(ctx, r.CapMinor, metric.WithAttributes(attribute.String("promotion", r.PromotionID), attribute.String("currency", r.Currency)))

		drift := cur - remaining

		switch {
		case drift == 0:
			_ = s.db.ClearPendingDrift(ctx, r.PromotionID, r.Currency)
			continue
		case drift > 0:
			_ = s.db.ClearPendingDrift(ctx, r.PromotionID, r.Currency)
		default:
			prev, seen, perr := s.db.PendingDrift(ctx, r.PromotionID, r.Currency)
			if perr != nil {
				s.logger.Error("read pending drift", "promotionId", r.PromotionID, "err", perr)
				continue
			}

			if !seen || prev != drift { // new or CHANGED shortfall → (re)start the two-tick confirmation
				if err := s.db.RecordPendingDrift(ctx, r.PromotionID, r.Currency, drift); err != nil {
					s.logger.Error("record pending drift", "promotionId", r.PromotionID, "err", err)
				}
				s.logger.Info("deferring give-back until confirmed next tick",
					"promotionId", r.PromotionID, "currency", r.Currency, "driftMinor", drift)
				continue
			}
		}

		if err := s.budget.AdjustBy(ctx, r.PromotionID, r.Currency, -drift); err != nil {
			s.logger.Error("adjust during reconcile", "promotionId", r.PromotionID, "err", err)
			continue
		}
		corrected++

		telemetry.Counter("payrail_reconciler_corrections_total").Add(ctx, 1)

		if err := s.db.LogDrift(ctx, r.PromotionID, r.Currency, drift); err != nil {
			s.logger.Error("log drift", "promotionId", r.PromotionID, "err", err)
		}

		s.logger.Warn("corrected counter drift",
			"promotionId", r.PromotionID, "currency", r.Currency, "driftMinor", drift)
	}

	if len(rows) > 0 {
		s.logger.Info("reconcile complete", "counters", len(rows), "corrected", corrected)
	}

	return nil
}

func (s *Service) HandleBudgetUpserted(ctx context.Context, m kafka.Message) error {
	var ev events.PromotionBudgetUpserted
	if err := json.Unmarshal(m.Value, &ev); err != nil {
		s.logger.Warn("skip unparseable budget event", "err", err)
		return nil
	}
	return s.seed(ctx, ev.PromotionID, ev.Currency)
}

func (s *Service) seed(ctx context.Context, promotionID, currency string) error {
	remaining, err := s.db.BudgetRemaining(ctx, promotionID, currency)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			return nil
		}
		return err
	}
	if err := s.budget.Seed(ctx, promotionID, currency, remaining); err != nil {
		return err
	}
	s.logger.Info("budget armed if missing (NX)", "promotionId", promotionID, "currency", currency, "remaining", remaining)
	return nil
}

func (s *Service) HandleActivated(ctx context.Context, m kafka.Message) error {
	var ev events.PromotionActivated
	if err := json.Unmarshal(m.Value, &ev); err != nil {
		s.logger.Warn("skip unparseable activation event", "err", err)
		return nil
	}
	rows, err := s.db.BudgetsForPromotion(ctx, ev.PromotionID)
	if err != nil {
		return err
	}
	for _, r := range rows {
		if err := s.budget.Seed(ctx, r.PromotionID, r.Currency, r.RemainingMinor); err != nil {
			return err
		}
	}
	s.logger.Info("armed missing budget counters (NX)", "promotionId", ev.PromotionID, "counters", len(rows))
	return nil
}

func (s *Service) RunPeriodic(ctx context.Context, interval time.Duration) {
	t := time.NewTicker(interval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			if err := s.ReconcileOnce(ctx); err != nil {
				s.logger.Error("reconcile failed", "err", err)
			}
		}
	}
}
