package exchange

import (
	"context"
	"time"
)

type Status int

const (
	Disconnected Status = iota
	Connecting
	Connected
	Err
)

func (s Status) String() string {
	switch s {
	case Connecting:
		return "connecting"
	case Connected:
		return "connected"
	case Err:
		return "error"
	default:
		return "disconnected"
	}
}

type Ticker struct {
	Exchange  string
	Symbol    string  // normalized, e.g. "BTC"
	Price     float64 // in USD
	Volume24h float64 // quote volume over 24h (USD)
	Timestamp time.Time
}

type Info struct {
	Name      string    `json:"name"`
	Status    string    `json:"status"`
	LastError string    `json:"last_error,omitempty"`
	LastSeen  time.Time `json:"last_seen"`
	Symbols   int       `json:"symbols"`
}

// Exchange connects to a single venue and emits normalized Tickers.
type Exchange interface {
	Name() string
	Start(ctx context.Context) error
	Subscribe(symbol string) error
	Tickers() <-chan Ticker
	Info() Info
}

func clampBackoff(d, max time.Duration) time.Duration {
	if d > max {
		return max
	}
	return d
}
