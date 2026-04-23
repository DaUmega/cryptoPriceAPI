package exchange

import (
	"context"
	"encoding/json"
	"log"
	"strings"
	"sync"
	"time"

	"github.com/gorilla/websocket"
)

const krakenWSURL = "wss://ws.kraken.com/v2"

type Kraken struct {
	mu       sync.RWMutex
	writeMu  sync.Mutex
	status   Status
	lastErr  string
	lastSeen time.Time
	symbols  map[string]bool // normalized symbols (e.g. "BTC")
	out      chan Ticker
	conn     *websocket.Conn
}

func NewKraken() *Kraken {
	return &Kraken{
		symbols: make(map[string]bool),
		out:     make(chan Ticker, 1000),
	}
}

func (k *Kraken) Name() string           { return "kraken" }
func (k *Kraken) Tickers() <-chan Ticker { return k.out }

func (k *Kraken) Info() Info {
	k.mu.RLock()
	defer k.mu.RUnlock()
	return Info{
		Name:      "kraken",
		Status:    k.status.String(),
		LastError: k.lastErr,
		LastSeen:  k.lastSeen,
		Symbols:   len(k.symbols),
	}
}

// Kraken WS v2 uses "SYMBOL/USD" format.
func (k *Kraken) toExchange(sym string) string { return sym + "/USD" }

func (k *Kraken) Start(ctx context.Context) error {
	go k.run(ctx)
	return nil
}

func (k *Kraken) Subscribe(symbol string) error {
	k.mu.Lock()
	k.symbols[symbol] = true
	conn := k.conn
	k.mu.Unlock()
	if conn != nil {
		return k.sendSubscribe(conn, []string{k.toExchange(symbol)})
	}
	return nil
}

type krakenSubscribeMsg struct {
	Method string          `json:"method"`
	Params krakenSubParams `json:"params"`
}
type krakenSubParams struct {
	Channel string   `json:"channel"`
	Symbol  []string `json:"symbol"`
}

func (k *Kraken) sendSubscribe(conn *websocket.Conn, krakenSyms []string) error {
	k.writeMu.Lock()
	defer k.writeMu.Unlock()
	return conn.WriteJSON(krakenSubscribeMsg{
		Method: "subscribe",
		Params: krakenSubParams{Channel: "ticker", Symbol: krakenSyms},
	})
}

func (k *Kraken) run(ctx context.Context) {
	backoff := time.Second
	for {
		if ctx.Err() != nil {
			return
		}
		k.setState(Connecting, "")

		conn, _, err := websocket.DefaultDialer.DialContext(ctx, krakenWSURL, nil)
		if err != nil {
			k.setState(Err, err.Error())
			select {
			case <-ctx.Done():
				return
			case <-time.After(backoff):
				backoff = clampBackoff(backoff*2, 5*time.Minute)
			}
			continue
		}
		backoff = time.Second
		k.mu.Lock()
		k.conn = conn
		syms := make([]string, 0, len(k.symbols))
		for s := range k.symbols {
			syms = append(syms, s)
		}
		k.mu.Unlock()
		k.setState(Connected, "")
		log.Printf("[kraken] connected; subscribing to %d symbol(s)", len(syms))

		if len(syms) > 0 {
			krakenSyms := make([]string, len(syms))
			for i, s := range syms {
				krakenSyms[i] = k.toExchange(s)
			}
			if err := k.sendSubscribe(conn, krakenSyms); err != nil {
				conn.Close()
				k.mu.Lock()
				k.conn = nil
				k.mu.Unlock()
				k.setState(Err, err.Error())
				continue
			}
		}

		if err := k.readLoop(ctx, conn); err != nil && ctx.Err() == nil {
			k.setState(Err, err.Error())
		}
		conn.Close()
		k.mu.Lock()
		k.conn = nil
		k.mu.Unlock()
	}
}

type krakenMsg struct {
	Channel string          `json:"channel"`
	Type    string          `json:"type"`
	Data    []krakenTickData `json:"data"`
}
type krakenTickData struct {
	Symbol string  `json:"symbol"` // e.g. "BTC/USD"
	Last   float64 `json:"last"`
	Volume float64 `json:"volume"` // 24h base volume
	Vwap   float64 `json:"vwap"`   // 24h VWAP
}

func (k *Kraken) readLoop(ctx context.Context, conn *websocket.Conn) error {
	for {
		if ctx.Err() != nil {
			return nil
		}
		conn.SetReadDeadline(time.Now().Add(60 * time.Second))
		_, msg, err := conn.ReadMessage()
		if err != nil {
			return err
		}

		k.mu.Lock()
		k.lastSeen = time.Now()
		k.mu.Unlock()

		var km krakenMsg
		if err := json.Unmarshal(msg, &km); err != nil {
			continue
		}
		if km.Channel != "ticker" {
			continue
		}

		for _, d := range km.Data {
			if !strings.HasSuffix(d.Symbol, "/USD") {
				continue
			}
			sym := strings.TrimSuffix(d.Symbol, "/USD")
			if d.Last <= 0 {
				continue
			}
			usdVol := d.Volume * d.Vwap
			select {
			case k.out <- Ticker{Exchange: "kraken", Symbol: sym, Price: d.Last, Volume24h: usdVol, Timestamp: time.Now()}:
			default:
			}
		}
	}
}

func (k *Kraken) setState(s Status, e string) {
	k.mu.Lock()
	k.status = s
	if e != "" {
		k.lastErr = e
	}
	k.mu.Unlock()
}

var _ Exchange = (*Kraken)(nil)
