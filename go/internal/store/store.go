package store

import (
	"context"

	"github.com/jackc/pgx/v5/pgxpool"
)

type Store struct {
	pool *pgxpool.Pool
}

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
				select 1 from "PlanPrice" x where x."country" = $1 and x."city" = "" and x."isActive" = true
			) then $1 else "US" end
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
	return out, nil
}
