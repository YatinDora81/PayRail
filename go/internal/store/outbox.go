package store

import (
	"context"
	"encoding/json"
	"sync"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/lucsky/cuid"
	"github.com/payrail/go/internal/outbox"
)

var (
	relayMu   sync.Mutex
	relayConn *pgxpool.Conn // held while we are (or believe we are) the leader
)

func (s *Store) TryAdvisoryLock(ctx context.Context, key int64) (bool, error) {
	relayMu.Lock()
	defer relayMu.Unlock()

	if relayConn != nil {
		// Verify the pinned session is alive; a dead session lost the lock.
		if err := relayConn.Ping(ctx); err == nil {
			return true, nil
		}

		relayConn.Release()
		relayConn = nil
	}

	conn, err := s.pool.Acquire(ctx)
	if err != nil {
		return false, err
	}

	var got bool
	if err := conn.QueryRow(ctx, `SELECT pg_try_advisory_lock($1)`, key).Scan(&got); err != nil {
		conn.Release()
		return false, err
	}

	if !got {
		conn.Release()
		return false, nil
	}

	relayConn = conn
	return true, nil
}

func (s *Store) UnpublishedOutbox(ctx context.Context, limit int) ([]outbox.OutboxRow, error) {
	const q = `
		SELECT "id", "topic", "partitionKey", "payload",
		       COALESCE("headers", '{}'::jsonb), "createdAt"
		FROM "OutboxEvent"
		WHERE "publishedAt" IS NULL
		ORDER BY "createdAt" ASC
		LIMIT $1`
	rows, err := s.pool.Query(ctx, q, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []outbox.OutboxRow
	for rows.Next() {
		var (
			r          outbox.OutboxRow
			rawHeaders []byte
		)
		if err := rows.Scan(&r.ID, &r.Topic, &r.PartitionKey, &r.Payload, &rawHeaders, &r.CreatedAt); err != nil {
			return nil, err
		}
		if len(rawHeaders) > 0 {
			_ = json.Unmarshal(rawHeaders, &r.Headers) // best-effort: headers are correlation, never correctness
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

func (s *Store) BumpAttemptsOrPark(ctx context.Context, id string, max int) error {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)

	var (
		attempts   int
		topic, key string
		payload    []byte
	)
	if err := tx.QueryRow(ctx, `
		UPDATE "OutboxEvent" SET "attempts" = "attempts" + 1
		WHERE "id" = $1
		RETURNING "attempts", "topic", "partitionKey", "payload"`, id).
		Scan(&attempts, &topic, &key, &payload); err != nil {
		return err
	}
	if attempts < max {
		return tx.Commit(ctx)
	}

	if _, err := tx.Exec(ctx, `
		INSERT INTO "DeadLetterEvent" ("id","source","topic","key","payload","reason")
		VALUES ($1,'outbox-relay',$2,$3,$4,'kafka publish failed after max attempts')`,
		cuid.New(), topic, key, payload); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `
		UPDATE "OutboxEvent" SET "publishedAt" = now() WHERE "id" = $1`, id); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

func (s *Store) MarkPublished(ctx context.Context, id string) error {
	_, err := s.pool.Exec(ctx, `
		UPDATE "OutboxEvent" SET "publishedAt" = now() WHERE "id" = $1`, id)
	return err
}

func (s *Store) PurgePublishedOutbox(ctx context.Context, olderThan time.Duration, limit int) (int64, error) {
	cutoff := time.Now().Add(-olderThan)
	ct, err := s.pool.Exec(ctx, `
		DELETE FROM "OutboxEvent"
		WHERE "id" IN (
			SELECT "id" FROM "OutboxEvent"
			WHERE "publishedAt" IS NOT NULL AND "publishedAt" < $1
			LIMIT $2
		)`, cutoff, limit)
	if err != nil {
		return 0, err
	}
	return ct.RowsAffected(), nil
}
