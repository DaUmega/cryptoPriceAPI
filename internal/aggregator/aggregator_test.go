package aggregator

import (
	"testing"
	"time"

	"cryptoapi/internal/exchange"
)

func tick(ex, sym string, price, vol float64, age time.Duration) exchange.Ticker {
	return exchange.Ticker{Exchange: ex, Symbol: sym, Price: price, Volume24h: vol, Timestamp: time.Now().Add(-age)}
}

func TestVWAPWeighted(t *testing.T) {
	a := New()
	a.Ingest(tick("a", "BTC", 100, 300, 0))
	a.Ingest(tick("b", "BTC", 102, 100, 0))
	p, ok := a.Get("BTC")
	if !ok || p.Price != 100.5 || p.Method != "vwap" || len(p.Sources) != 2 {
		t.Fatalf("vwap = %+v ok=%v", p, ok)
	}
}

func TestMedianEvenCount(t *testing.T) {
	// True median of 100,102,104,106 is 103, not the upper median 104.
	// With equal volumes, the outlier filter uses the median, so a wrong
	// median can discard a legitimate source. All four are within 5%
	// of 103, so none should be discarded.
	a := New()
	for i, price := range []float64{100, 102, 104, 106} {
		a.Ingest(tick("ex"+string(rune('a'+i)), "BTC", price, 100, 0))
	}
	p, ok := a.Get("BTC")
	if !ok || p.Price != 103 || len(p.Sources) != 4 {
		t.Fatalf("even median vwap = %+v ok=%v", p, ok)
	}
}

func TestOutlierDiscardedAndSingleMethod(t *testing.T) {
	a := New()
	a.Ingest(tick("a", "BTC", 100, 100, 0))
	a.Ingest(tick("b", "BTC", 101, 100, 0))
	a.Ingest(tick("c", "BTC", 200, 100, 0)) // >5% from median 101, discarded
	p, ok := a.Get("BTC")
	if !ok || len(p.Sources) != 2 || p.Method != "vwap" {
		t.Fatalf("outlier = %+v ok=%v", p, ok)
	}
	b := New()
	b.Ingest(tick("a", "BTC", 100, 100, 0))
	p, ok = b.Get("BTC")
	if !ok || p.Method != "single" {
		t.Fatalf("one source must report method single, got %+v", p)
	}
}

func TestStalePruned(t *testing.T) {
	a := New()
	a.Ingest(tick("a", "BTC", 100, 100, 31*time.Second))
	a.Ingest(tick("b", "BTC", 200, 100, 0))
	if p, ok := a.Get("BTC"); !ok || len(p.Sources) != 1 || p.Sources[0].Exchange != "b" {
		t.Fatalf("stale should be pruned, got %+v ok=%v", p, ok)
	}
	if syms := a.Symbols(); len(syms) != 1 {
		t.Fatalf("symbols = %v", syms)
	}
	c := New()
	c.Ingest(tick("a", "OLD", 100, 100, 31*time.Second))
	if _, ok := c.Get("OLD"); ok {
		t.Fatal("fully stale symbol should return no price")
	}
	if syms := c.Symbols(); len(syms) != 0 {
		t.Fatalf("stale symbol should be pruned from Symbols, got %v", syms)
	}
}

func TestZeroVolumeFallback(t *testing.T) {
	a := New()
	a.Ingest(tick("a", "BTC", 100, 0, 0)) // Bitstamp trades have no 24h volume
	p, ok := a.Get("BTC")
	if !ok || p.Price != 100 || p.Volume24h != 1 {
		t.Fatalf("zero volume should fall back to weight 1, got %+v", p)
	}
}
