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

const bybitWSURL = "wss://stream.bybit.com/v5/public/spot"

type Bybit struct {
	mu       sync.RWMutex
	writeMu  sync.Mutex
	status   Status
	lastErr  string
	lastSeen time.Time
	symbols  map[string]bool
	out      chan Ticker
	conn     *websocket.Conn
}

func NewBybit() *Bybit {
	return &Bybit{
		symbols: make(map[string]bool),
		out:     make(chan Ticker, 1000),
	}
}

func (b *Bybit) Name() string           { return "bybit" }
func (b *Bybit) Tickers() <-chan Ticker { return b.out }

func (b *Bybit) Info() Info {
	b.mu.RLock()
	defer b.mu.RUnlock()
	return Info{
		Name:      "bybit",
		Status:    b.status.String(),
		LastError: b.lastErr,
		LastSeen:  b.lastSeen,
		Symbols:   len(b.symbols),
	}
}

func (b *Bybit) toExchange(sym string) string { return sym + "USDT" }

func (b *Bybit) fromExchange(s string) (string, bool) {
	if !strings.HasSuffix(s, "USDT") {
		return "", false
	}
	return strings.TrimSuffix(s, "USDT"), true
}

func (b *Bybit) Start(ctx context.Context) error {
	go b.run(ctx)
	return nil
}

func (b *Bybit) Subscribe(symbol string) error {
	b.mu.Lock()
	b.symbols[symbol] = true
	conn := b.conn
	b.mu.Unlock()
	if conn != nil {
		return b.sendSubscribe(conn, []string{symbol})
	}
	return nil
}

type bybitSubMsg struct {
	Op   string   `json:"op"`
	Args []string `json:"args"`
}

func (b *Bybit) sendSubscribe(conn *websocket.Conn, symbols []string) error {
	args := make([]string, len(symbols))
	for i, s := range symbols {
		args[i] = "tickers." + b.toExchange(s)
	}
	b.writeMu.Lock()
	defer b.writeMu.Unlock()
	return conn.WriteJSON(bybitSubMsg{Op: "subscribe", Args: args})
}

func (b *Bybit) run(ctx context.Context) {
	backoff := time.Second
	for {
		if ctx.Err() != nil {
			return
		}
		b.setState(Connecting, "")

		conn, resp, err := websocket.DefaultDialer.DialContext(ctx, bybitWSURL, nil)
		if err != nil {
			if resp != nil {
				log.Printf("[bybit] dial failed: HTTP %d %s", resp.StatusCode, resp.Status)
			}
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

		b.mu.Lock()
		b.conn = conn
		syms := make([]string, 0, len(b.symbols))
		for s := range b.symbols {
			syms = append(syms, s)
		}
		b.mu.Unlock()
		b.setState(Connected, "")
		log.Printf("[bybit] connected; subscribing to %d symbol(s)", len(syms))

		if len(syms) > 0 {
			for i := 0; i < len(syms); i += 10 {
				end := i + 10
				if end > len(syms) {
					end = len(syms)
				}
				if err := b.sendSubscribe(conn, syms[i:end]); err != nil {
					break
				}
			}
		}

		go b.pingLoop(ctx, conn)

		if err := b.readLoop(ctx, conn); err != nil && ctx.Err() == nil {
			b.setState(Err, err.Error())
		}
		conn.Close()
		b.mu.Lock()
		b.conn = nil
		b.mu.Unlock()
	}
}

func (b *Bybit) pingLoop(ctx context.Context, conn *websocket.Conn) {
	t := time.NewTicker(20 * time.Second)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			b.writeMu.Lock()
			err := conn.WriteJSON(map[string]string{"op": "ping"})
			b.writeMu.Unlock()
			if err != nil {
				return
			}
		}
	}
}

type bybitMsg struct {
	Topic string        `json:"topic"`
	Data  bybitTickData `json:"data"`
}
type bybitTickData struct {
	Symbol      string `json:"symbol"`
	LastPrice   string `json:"lastPrice"`
	Turnover24h string `json:"turnover24h"`
}

func (b *Bybit) readLoop(ctx context.Context, conn *websocket.Conn) error {
	for {
		if ctx.Err() != nil {
			return nil
		}
		conn.SetReadDeadline(time.Now().Add(40 * time.Second))
		_, msg, err := conn.ReadMessage()
		if err != nil {
			return err
		}

		b.mu.Lock()
		b.lastSeen = time.Now()
		b.mu.Unlock()

		var m bybitMsg
		if err := json.Unmarshal(msg, &m); err != nil {
			continue
		}
		if !strings.HasPrefix(m.Topic, "tickers.") {
			continue
		}

		sym, ok := b.fromExchange(m.Data.Symbol)
		if !ok {
			continue
		}
		price, _ := strconv.ParseFloat(m.Data.LastPrice, 64)
		vol, _ := strconv.ParseFloat(m.Data.Turnover24h, 64)
		if price <= 0 {
			continue
		}
		select {
		case b.out <- Ticker{Exchange: "bybit", Symbol: sym, Price: price, Volume24h: vol, Timestamp: time.Now()}:
		default:
		}
	}
}

func (b *Bybit) setState(s Status, e string) {
	b.mu.Lock()
	b.status = s
	if e != "" {
		b.lastErr = e
	}
	b.mu.Unlock()
}

var _ Exchange = (*Bybit)(nil)
