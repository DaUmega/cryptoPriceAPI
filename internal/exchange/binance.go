package exchange

import (
	"context"
	"encoding/json"
	"log"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/gorilla/websocket"
)

// Connect to the combined stream endpoint; subscribe dynamically via SUBSCRIBE messages.
var binanceWSURL = "wss://stream.binance.com:9443/stream"

type Binance struct {
	mu       sync.RWMutex
	writeMu  sync.Mutex // serializes all WebSocket writes
	status   Status
	lastErr  string
	lastSeen time.Time
	symbols  map[string]bool
	out      chan Ticker
	conn     *websocket.Conn
	nextID   int
}

func NewBinance() *Binance {
	return &Binance{
		symbols: make(map[string]bool),
		out:     make(chan Ticker, 2000),
	}
}

func (b *Binance) Name() string           { return "binance" }
func (b *Binance) Tickers() <-chan Ticker { return b.out }

func (b *Binance) Info() Info {
	b.mu.RLock()
	defer b.mu.RUnlock()
	return Info{
		Name:      "binance",
		Status:    b.status.String(),
		LastError: b.lastErr,
		LastSeen:  b.lastSeen,
		Symbols:   len(b.symbols),
	}
}

func (b *Binance) Start(ctx context.Context) error {
	go b.run(ctx)
	return nil
}

func (b *Binance) Subscribe(symbol string) error {
	b.mu.Lock()
	b.symbols[symbol] = true
	conn := b.conn
	b.mu.Unlock()
	if conn != nil {
		return b.sendSubscribe(conn, []string{symbol})
	}
	return nil
}

func (b *Binance) toExchange(sym string) string { return sym + "USDT" }

func (b *Binance) fromExchange(s string) (string, bool) {
	if !strings.HasSuffix(s, "USDT") {
		return "", false
	}
	return strings.TrimSuffix(s, "USDT"), true
}

type binanceSubMsg struct {
	Method string   `json:"method"`
	Params []string `json:"params"`
	ID     int      `json:"id"`
}

func (b *Binance) sendSubscribe(conn *websocket.Conn, symbols []string) error {
	params := make([]string, len(symbols))
	for i, s := range symbols {
		params[i] = strings.ToLower(b.toExchange(s)) + "@ticker"
	}
	b.writeMu.Lock()
	defer b.writeMu.Unlock()
	b.nextID++
	return conn.WriteJSON(binanceSubMsg{Method: "SUBSCRIBE", Params: params, ID: b.nextID})
}

type binanceCombinedMsg struct {
	Stream string      `json:"stream"`
	Data   binanceTick `json:"data"`
}

// Binance ticker fields use mixed case. Go's json decoder matches case-insensitively,
// so "C" (stats-window close ms, number) would collide with "c" (last price, string)
// and "Q" (last trade qty) with "q" (total quote volume). Explicit absorber fields
// force exact-match routing and prevent type errors that silently drop messages.
type binanceTick struct {
	Symbol    string      `json:"s"`
	Close     string      `json:"c"`
	CloseTime json.Number `json:"C"` // absorbs uppercase C (stats window close ms)
	Quote     string      `json:"q"`
	LastQty   string      `json:"Q"` // absorbs uppercase Q (last trade quantity)
}

func (b *Binance) run(ctx context.Context) {
	backoff := time.Second
	for {
		if ctx.Err() != nil {
			return
		}
		b.setState(Connecting, "")

		conn, _, err := websocket.DefaultDialer.DialContext(ctx, binanceWSURL, nil)
		if err != nil {
			b.setState(Err, err.Error())
			select {
			case <-ctx.Done():
				return
			case <-time.After(backoff):
				backoff = clampBackoff(backoff*2, 5*time.Minute)
			}
			continue
		}
		backoff = time.Second

		// WriteControl (pong) is safe to call concurrently with WriteJSON per gorilla docs.
		conn.SetPingHandler(func(data string) error {
			conn.SetReadDeadline(time.Now().Add(60 * time.Second)) //nolint:errcheck
			return conn.WriteControl(websocket.PongMessage, []byte(data), time.Now().Add(5*time.Second))
		})

		b.mu.Lock()
		b.conn = conn
		syms := make([]string, 0, len(b.symbols))
		for s := range b.symbols {
			syms = append(syms, s)
		}
		b.mu.Unlock()
		b.setState(Connected, "")
		log.Printf("[binance] connected; subscribing to %d symbol(s)", len(syms))

		if len(syms) > 0 {
			if err := b.sendSubscribe(conn, syms); err != nil {
				conn.Close()
				b.mu.Lock()
				b.conn = nil
				b.mu.Unlock()
				b.setState(Err, err.Error())
				continue
			}
		}

		if err := b.readLoop(ctx, conn); err != nil && ctx.Err() == nil {
			b.setState(Err, err.Error())
			log.Printf("[binance] readLoop error: %v", err)
		}
		conn.Close()
		b.mu.Lock()
		b.conn = nil
		b.mu.Unlock()
	}
}

func (b *Binance) readLoop(ctx context.Context, conn *websocket.Conn) error {
	for {
		if ctx.Err() != nil {
			return nil
		}
		conn.SetReadDeadline(time.Now().Add(60 * time.Second))
		_, msg, err := conn.ReadMessage()
		if err != nil {
			return err
		}

		b.mu.Lock()
		b.lastSeen = time.Now()
		b.mu.Unlock()

		var combined binanceCombinedMsg
		if err := json.Unmarshal(msg, &combined); err != nil {
			continue
		}
		if !strings.HasSuffix(combined.Stream, "@ticker") {
			continue
		}

		tick := combined.Data
		if tick.Symbol == "" {
			continue
		}

		sym, ok := b.fromExchange(tick.Symbol)
		if !ok {
			continue
		}

		b.mu.RLock()
		watched := b.symbols[sym]
		b.mu.RUnlock()
		if !watched {
			continue
		}

		price, _ := strconv.ParseFloat(tick.Close, 64)
		vol, _ := strconv.ParseFloat(tick.Quote, 64)
		if price <= 0 {
			continue
		}
		select {
		case b.out <- Ticker{Exchange: "binance", Symbol: sym, Price: price, Volume24h: vol, Timestamp: time.Now()}:
		default:
		}
	}
}

func (b *Binance) setState(s Status, e string) {
	b.mu.Lock()
	b.status = s
	if e != "" {
		b.lastErr = e
	}
	b.mu.Unlock()
}

var _ Exchange = (*Binance)(nil)
