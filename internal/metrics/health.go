package metrics

import (
	"context"
	"encoding/json"
	"net/http"
	"time"

	"github.com/yourname/raw-cacher-go/internal/storage"
)

type HealthHandler struct {
	Store *storage.Store
}

type healthResponse struct {
	Status string `json:"status"`
}

func (h *HealthHandler) HealthCheckHandler() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		ctx, cancel := context.WithTimeout(r.Context(), 2*time.Second)
		defer cancel()

		if err := h.Store.Ping(ctx); err != nil {
			writeHealth(w, http.StatusServiceUnavailable, "down")
			return
		}

		writeHealth(w, http.StatusOK, "up")
	}
}

func writeHealth(w http.ResponseWriter, code int, status string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(healthResponse{Status: status})
}
