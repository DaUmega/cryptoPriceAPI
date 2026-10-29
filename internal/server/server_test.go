package server

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"cryptoapi/internal/aggregator"
	"cryptoapi/internal/exchange"
)

type fakeMgr struct {
	infos      []exchange.Info
	connected  int
	subscribed []string
}

func (m *fakeMgr) Infos() []exchange.Info { return m.infos }
func (m *fakeMgr) Subscribe(s string)     { m.subscribed = append(m.subscribed, s) }
func (m *fakeMgr) ConnectedCount() int    { return m.connected }

func TestHealthDegradedWhenNoExchangeConnected(t *testing.T) {
	s := New(aggregator.New(), &fakeMgr{})
	req := httptest.NewRequest("GET", "/health", nil)
	rec := httptest.NewRecorder()
	s.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("health with 0 connected should be 503, got %d %s", rec.Code, rec.Body.String())
	}
	s2 := New(aggregator.New(), &fakeMgr{connected: 1})
	rec = httptest.NewRecorder()
	s2.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("health with 1 connected should be 200, got %d", rec.Code)
	}
}

func TestGetPriceCachedReturnsImmediately(t *testing.T) {
	agg := aggregator.New()
	agg.Ingest(exchange.Ticker{Exchange: "kraken", Symbol: "XMR", Price: 500, Volume24h: 1000, Timestamp: time.Now()})
	s := New(agg, &fakeMgr{connected: 9})
	start := time.Now()
	req := httptest.NewRequest("GET", "/price/xmr", nil)
	rec := httptest.NewRecorder()
	s.Handler().ServeHTTP(rec, req)
	if time.Since(start) > 2*time.Second {
		t.Fatalf("cached price took too long: %v", time.Since(start))
	}
	if rec.Code != http.StatusOK {
		t.Fatalf("cached price should be 200, got %d %s", rec.Code, rec.Body.String())
	}
	var p aggregator.Price
	if err := json.Unmarshal(rec.Body.Bytes(), &p); err != nil || p.Symbol != "XMR" || p.Method != "single" {
		t.Fatalf("price = %+v err=%v", p, err)
	}
}

func TestGetPriceUnknownReturns404(t *testing.T) {
	s := New(aggregator.New(), &fakeMgr{connected: 9})
	req := httptest.NewRequest("GET", "/price/BADCOIN999", nil)
	rec := httptest.NewRecorder()
	s.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("unknown symbol should be 404, got %d", rec.Code)
	}
}

func TestStatusHealthyFlag(t *testing.T) {
	s := New(aggregator.New(), &fakeMgr{infos: []exchange.Info{{Name: "kraken", Status: "connected"}}})
	req := httptest.NewRequest("GET", "/status", nil)
	rec := httptest.NewRecorder()
	s.Handler().ServeHTTP(rec, req)
	var body map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil || body["healthy"] != true {
		t.Fatalf("status healthy = %v err=%v", body, err)
	}
}
