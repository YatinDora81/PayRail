package outbox

import (
	"context"
	"log/slog"
	"sync"
	"time"

	"github.com/payrail/go/internal/kafka"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/metric"

)

const (
	relayLockKey int64 = 0x5041_5952_4C0B
	// maxAttempts is the N in N-strikes parking: a row Kafka keeps rejecting
	// is parked to DeadLetterEvent and only ITS key's chain skips
	maxAttempts = 10

	drainEvery = time.Second // leader cadence between batches
)

type OutboxRow struct {
	ID           string
	Topic        string
	PartitionKey string
	Payload      []byte
	Headers      map[string]string // traceparent + eventId, stored at enqueue
	CreatedAt    time.Time
}

type Publisher interface {
	Publish(ctx context.Context, topic, key string, payload []byte, headers map[string]string) error
}

type Store interface {
	TryAdvisoryLock(ctx context.Context, key int64) (bool, error)
	UnpublishedOutbox(ctx context.Context, limit int) ([]OutboxRow, error)
	MarkPublished(ctx context.Context, id string) error
	BumpAttemptsOrPark(ctx context.Context, id string, max int) error
}

type Relay struct {
	store     Store
	kafka     Publisher
	log       *slog.Logger
	gaugeOnce sync.Once
	oldest    metric.Int64Gauge
}

type writerPublisher struct {
	w *kafka.Writer
}

func (p writerPublisher) Publish(ctx context.Context, topic, key string, payload []byte, _ map[string]string) error {
	return p.w.Publish(ctx, topic, key, payload)
}

func NewRelay(db Store, w *kafka.Writer, log *slog.Logger) *Relay {
	return &Relay{
		store: db, kafka: writerPublisher{w}, log: log,
	}
}

func (r *Relay) RunElected(ctx context.Context) {
	for ctx.Err() == nil {
		if got, _ := r.store.TryAdvisoryLock(ctx, relayLockKey); !got { // session-scoped: leader crash ⇒ lock dies ⇒ a standby wins next tick
			r.sleep(ctx, 5*time.Second) // standby: wait, retry
			continue
		}
		r.drainLoop(ctx)
	}
}

func (r *Relay) drainLoop(ctx context.Context) {
	r.log.Info("outbox relay elected leader")
	ticker := time.NewTicker(drainEvery)
	defer ticker.Stop()
	for {
		if err := r.drain(ctx); err != nil {
			r.log.Error("outbox drain failed; standing down", "err", err)
			return // re-elect: the advisory lock follows the (possibly dead) session
		}
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

func (r *Relay) drain(ctx context.Context) error {
	batch, err := r.store.UnpublishedOutbox(ctx, 500)
	if err != nil {
		return err
	}

	r.gaugeOldest(ctx, batch)

	var wg sync.WaitGroup
	for _, rows := range groupByKey(batch) { // per-key buckets
		wg.Add(1)
		go func(rows []OutboxRow) {
			defer wg.Done()
			for _, e := range rows {
				if err := r.kafka.Publish(ctx, e.Topic, e.PartitionKey, e.Payload, e.Headers); err != nil {
					_ = r.store.BumpAttemptsOrPark(ctx, e.ID, maxAttempts)
					return
				}
				if err := r.store.MarkPublished(ctx, e.ID); err != nil {
					return
				}
			}
		}(rows)
	}
	wg.Wait()
	return nil
}

func groupByKey(batch []OutboxRow) map[string][]OutboxRow {
	out := make(map[string][]OutboxRow, len(batch))
	for _, row := range batch {
		out[row.PartitionKey] = append(out[row.PartitionKey], row)
	}
	return out
}

func (r *Relay) gaugeOldest(ctx context.Context, batch []OutboxRow) {
	r.gaugeOnce.Do(func() {
		g, err := otel.Meter("payrail").Int64Gauge("payrail_outbox_oldest_unpublished_seconds")
		if err != nil {
			r.log.Error("outbox gauge init", "err", err)
			return
		}
		r.oldest = g
	})
	if r.oldest == nil {
		return
	}
	var oldest int64
	for _, row := range batch {
		if age := int64(time.Since(row.CreatedAt).Seconds()); age > oldest {
			oldest = age
		}
	}
	r.oldest.Record(ctx, oldest)
}

func (r *Relay) sleep(ctx context.Context, d time.Duration) {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
	case <-t.C:
	}
}
