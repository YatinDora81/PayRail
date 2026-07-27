package settlement

import (
	"log/slog"

	"github.com/payrail/go/internal/store"
)

type Service struct {
	db     *store.Store
	logger *slog.Logger
}

func NewService(db *store.Store, logger *slog.Logger) *Service {
	return &Service{db: db, logger: logger}
}
