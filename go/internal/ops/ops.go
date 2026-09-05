package ops

import (
	"context"
	"log/slog"
	"net/http"
	"os"
	"time"

	"github.com/payrail/go/internal/telemetry"
)

func Serve(ctx context.Context, ready func(context.Context) error, log *slog.Logger) error {
	port := os.Getenv("OPS_PORT")

	if port == "" {
		port = "9091"
	}

	mux := http.NewServeMux()

	mux.HandleFunc("/healthz", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	})

	mux.HandleFunc("/readyz", func(w http.ResponseWriter, r *http.Request) {
		if ready != nil {
			if err := ready(r.Context()); err != nil {
				http.Error(w, err.Error(), http.StatusServiceUnavailable)
				return
			}
		}
		w.WriteHeader(http.StatusOK)
	})

	mux.Handle("/metrics", telemetry.MetricsHandler())
	
	srv := &http.Server{Addr: ":" + port, Handler: mux, ReadHeaderTimeout: 5 * time.Second}
	
	errc := make(chan error, 1)
	go func() { errc <- srv.ListenAndServe() }()
	
	select {
	case <-ctx.Done():
		shCtx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		_ = srv.Shutdown(shCtx)
		return nil
	case err := <-errc:
		log.Error("ops server", "err", err)
		return err
	}
}
