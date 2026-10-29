package aggregator

import (
	"math"
	"sort"
	"sync"
	"time"

	"cryptoapi/internal/exchange"
)

const (
	staleThreshold = 30 * time.Second
	outlierPct     = 0.05 // prices >5% from median are discarded
)

type Source struct {
	Exchange  string    `json:"exchange"`
	Price     float64   `json:"price"`
	Volume24h float64   `json:"volume_24h_usd"`
	UpdatedAt time.Time `json:"updated_at"`
}

type Price struct {
	Symbol    string    `json:"symbol"`
	Price     float64   `json:"price_usd"`
	Volume24h float64   `json:"volume_24h_usd"`
	Sources   []Source  `json:"sources"`
	Method    string    `json:"method"`
	UpdatedAt time.Time `json:"updated_at"`
}

type point struct {
	price     float64
	volume    float64
	exchange  string
	timestamp time.Time
}

type Aggregator struct {
	mu     sync.Mutex
	latest map[string]map[string]point // symbol -> exchange -> latest point
}

func New() *Aggregator {
	return &Aggregator{latest: make(map[string]map[string]point)}
}

func (a *Aggregator) Ingest(t exchange.Ticker) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.latest[t.Symbol] == nil {
		a.latest[t.Symbol] = make(map[string]point)
	}
	a.latest[t.Symbol][t.Exchange] = point{price: t.Price, volume: t.Volume24h, exchange: t.Exchange, timestamp: t.Timestamp}
}

// pruneLocked drops stale points and symbols with no fresh point left.
// Caller must hold a.mu.
func (a *Aggregator) pruneLocked(now time.Time) {
	for sym, pts := range a.latest {
		for ex, p := range pts {
			if now.Sub(p.timestamp) > staleThreshold {
				delete(pts, ex)
			}
		}
		if len(pts) == 0 {
			delete(a.latest, sym)
		}
	}
}

func median(prices []float64) float64 {
	n := len(prices)
	if n%2 == 1 {
		return prices[n/2]
	}
	return (prices[n/2-1] + prices[n/2]) / 2
}

// Get returns the aggregated price for a symbol across all fresh sources.
func (a *Aggregator) Get(symbol string) (Price, bool) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.pruneLocked(time.Now())
	pts, ok := a.latest[symbol]
	if !ok || len(pts) == 0 {
		return Price{}, false
	}
	fresh := make([]point, 0, len(pts))
	for _, p := range pts {
		fresh = append(fresh, p)
	}
	prices := make([]float64, len(fresh))
	for i, p := range fresh {
		prices[i] = p.price
	}
	sort.Float64s(prices)
	med := median(prices)

	var weightedSum, totalVol float64
	sources := make([]Source, 0, len(fresh))
	for _, p := range fresh {
		if med > 0 && math.Abs(p.price-med)/med >= outlierPct {
			continue
		}
		vol := p.volume
		if vol <= 0 {
			vol = 1
		}
		weightedSum += p.price * vol
		totalVol += vol
		sources = append(sources, Source{Exchange: p.exchange, Price: p.price, Volume24h: p.volume, UpdatedAt: p.timestamp})
	}
	if totalVol == 0 || len(sources) == 0 {
		return Price{}, false
	}
	method := "vwap"
	if len(sources) == 1 {
		method = "single"
	}
	return Price{Symbol: symbol, Price: weightedSum / totalVol, Volume24h: totalVol, Sources: sources, Method: method, UpdatedAt: time.Now()}, true
}

// Symbols returns symbols with at least one fresh point, not ever-seen symbols.
func (a *Aggregator) Symbols() []string {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.pruneLocked(time.Now())
	syms := make([]string, 0, len(a.latest))
	for s := range a.latest {
		syms = append(syms, s)
	}
	return syms
}
