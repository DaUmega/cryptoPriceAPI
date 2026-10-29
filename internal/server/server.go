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
	return &Server{agg: agg, mgr: mgr, startedAt: time.Now(), version: "1.1.0"}
}

func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /health", s.health)
	mux.HandleFunc("GET /status", s.status)
	mux.HandleFunc("GET /price/{symbol}", s.getPrice)
	return cors(mux)
}

func (s *Server) health(w http.ResponseWriter, r *http.Request) {
	if s.mgr.ConnectedCount() == 0 {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"status": "degraded", "version": s.version})
		return
	}
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
	s.mgr.Subscribe(sym)

	// Fast path: a fresh cached price returns immediately. The old code
	// waited the full 5 seconds whenever not every connected exchange had
	// reported, which hit every coin some venues do not list (notably XMR).
	if p, ok := s.agg.Get(sym); ok {
		writeJSON(w, http.StatusOK, p)
		return
	}

	// Otherwise wait for a quorum, not for every venue. Two sources are
	// enough to return, and a single source gets a 1 second grace period
	// for a second source to arrive, then returns instead of waiting 5s.
	deadline := time.Now().Add(5 * time.Second)
	tick := time.NewTicker(150 * time.Millisecond)
	defer tick.Stop()
	var best aggregator.Price
	var firstSeen time.Time
	for time.Now().Before(deadline) {
		select {
		case <-r.Context().Done():
			return
		case <-tick.C:
		}
		p, ok := s.agg.Get(sym)
		if !ok {
			continue
		}
		best = p
		if len(p.Sources) >= 2 || len(p.Sources) >= s.mgr.ConnectedCount() {
			break
		}
		if firstSeen.IsZero() {
			firstSeen = time.Now()
		} else if time.Since(firstSeen) >= time.Second {
			break
		}
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
