package server

import (
	"encoding/json"
	"net/http"
	"strings"
	"time"

	"cryptoapi/internal/aggregator"
	"cryptoapi/internal/exchange"
)

type ExchangeManager interface {
	Infos() []exchange.Info
	Subscribe(symbol string)
	ConnectedCount() int
}

type Server struct {
	agg       *aggregator.Aggregator
	mgr       ExchangeManager
	startedAt time.Time
	version   string
}

func New(agg *aggregator.Aggregator, mgr ExchangeManager) *Server {
	return &Server{
		agg:       agg,
		mgr:       mgr,
		startedAt: time.Now(),
		version:   "2.0.0",
	}
}

func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /health", s.health)
	mux.HandleFunc("GET /status", s.status)
	mux.HandleFunc("GET /price/{symbol}", s.getPrice)
	return cors(mux)
}

func (s *Server) health(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok", "version": s.version})
}

func (s *Server) status(w http.ResponseWriter, r *http.Request) {
	infos := s.mgr.Infos()
	healthy := false
	for _, inf := range infos {
		if inf.Status == "connected" {
			healthy = true
			break
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"uptime":        time.Since(s.startedAt).Truncate(time.Second).String(),
		"healthy":       healthy,
		"exchanges":     infos,
		"tracked_coins": len(s.agg.Symbols()),
		"version":       s.version,
	})
}

func (s *Server) getPrice(w http.ResponseWriter, r *http.Request) {
	sym := normalize(r.PathValue("symbol"))
	if sym == "" {
		writeErr(w, http.StatusBadRequest, "missing symbol")
		return
	}

	// Subscribe all exchanges to this symbol (no-op if already subscribed).
	s.mgr.Subscribe(sym)

	// Poll until all connected exchanges have reported or the deadline passes,
	// then return the best data accumulated so far.
	deadline := time.Now().Add(5 * time.Second)
	var best aggregator.Price
	for time.Now().Before(deadline) {
		if p, ok := s.agg.Get(sym); ok {
			best = p
			if len(p.Sources) >= s.mgr.ConnectedCount() {
				break
			}
		}
		time.Sleep(150 * time.Millisecond)
	}

	if len(best.Sources) > 0 {
		writeJSON(w, http.StatusOK, best)
		return
	}

	writeErr(w, http.StatusNotFound, "no price data for "+sym+"; symbol may not be supported by any exchange")
}

func normalize(s string) string {
	return strings.ToUpper(strings.TrimSpace(s))
}

func writeJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	enc.Encode(v) //nolint:errcheck
}

func writeErr(w http.ResponseWriter, code int, msg string) {
	writeJSON(w, code, map[string]string{"error": msg})
}

func cors(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Access-Control-Allow-Origin", "*")
		w.Header().Set("Access-Control-Allow-Methods", "GET, OPTIONS")
		w.Header().Set("Access-Control-Allow-Headers", "Content-Type")
		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		next.ServeHTTP(w, r)
	})
}
