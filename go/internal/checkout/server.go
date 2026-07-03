package checkout

import (
	"context"
	"log/slog"
	"net/http"

	"github.com/payrail/go/internal/middleware"
)

type Pingable interface {
	Ping(ctx context.Context) error
}

func NewRouter(h *Handler, db, rdb Pingable, userJwtSecret string, allow middleware.AllowFunc, logger *slog.Logger) http.Handler {
	api := http.NewServeMux()

	return api
}
