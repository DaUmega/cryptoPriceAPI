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

const coinbaseWSURL = "wss://advanced-trade-ws.coinbase.com"

type Coinbase struct {
	mu       sync.RWMutex
	writeMu  sync.Mutex
	status   Status
	lastErr  string
	lastSeen time.Time
	symbols  map[string]bool
	out      chan Ticker
	conn     *websocket.Conn
}

func NewCoinbase() *Coinbase {
	return &Coinbase{
		symbols: make(map[string]bool),
		out:     make(chan Ticker, 1000),
	}
}

func (c *Coinbase) Name() string           { return "coinbase" }
func (c *Coinbase) Tickers() <-chan Ticker { return c.out }

func (c *Coinbase) Info() Info {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return Info{
		Name:      "coinbase",
		Status:    c.status.String(),
		LastError: c.lastErr,
		LastSeen:  c.lastSeen,
		Symbols:   len(c.symbols),
	}
}

// Coinbase Advanced Trade WS uses "SYMBOL-USD" format.
func (c *Coinbase) toExchange(sym string) string { return sym + "-USD" }

func (c *Coinbase) Start(ctx context.Context) error {
	go c.run(ctx)
	return nil
}

func (c *Coinbase) Subscribe(symbol string) error {
	c.mu.Lock()
	c.symbols[symbol] = true
	conn := c.conn
	c.mu.Unlock()
	if conn != nil {
		return c.sendSubscribe(conn, []string{c.toExchange(symbol)})
	}
	return nil
}

type cbSubscribeMsg struct {
	Type       string   `json:"type"`
	Channel    string   `json:"channel"`
	ProductIDs []string `json:"product_ids"`
}

func (c *Coinbase) sendSubscribe(conn *websocket.Conn, productIDs []string) error {
	c.writeMu.Lock()
	defer c.writeMu.Unlock()
	return conn.WriteJSON(cbSubscribeMsg{
		Type:       "subscribe",
		Channel:    "ticker",
		ProductIDs: productIDs,
	})
}

func (c *Coinbase) run(ctx context.Context) {
	backoff := time.Second
	for {
		if ctx.Err() != nil {
			return
		}
		c.setState(Connecting, "")

		conn, resp, err := websocket.DefaultDialer.DialContext(ctx, coinbaseWSURL, nil)
		if err != nil {
			if resp != nil {
				log.Printf("[coinbase] dial failed: HTTP %d %s", resp.StatusCode, resp.Status)
			}
			c.setState(Err, err.Error())
			select {
			case <-ctx.Done():
				return
			case <-time.After(backoff):
				backoff = clampBackoff(backoff*2, 5*time.Minute)
			}
			continue
		}
		backoff = time.Second
		c.mu.Lock()
		c.conn = conn
		syms := make([]string, 0, len(c.symbols))
		for s := range c.symbols {
			syms = append(syms, s)
		}
		c.mu.Unlock()
		c.setState(Connected, "")
		log.Printf("[coinbase] connected; subscribing to %d symbol(s)", len(syms))

		if len(syms) > 0 {
			products := make([]string, len(syms))
			for i, s := range syms {
				products[i] = c.toExchange(s)
			}
			if err := c.sendSubscribe(conn, products); err != nil {
				conn.Close()
				c.mu.Lock()
				c.conn = nil
				c.mu.Unlock()
				c.setState(Err, err.Error())
				continue
			}
		}

		if err := c.readLoop(ctx, conn); err != nil && ctx.Err() == nil {
			c.setState(Err, err.Error())
		}
		conn.Close()
		c.mu.Lock()
		c.conn = nil
		c.mu.Unlock()
	}
}

type cbMsg struct {
	Channel string    `json:"channel"`
	Events  []cbEvent `json:"events"`
}
type cbEvent struct {
	Type    string     `json:"type"`
	Tickers []cbTicker `json:"tickers"`
}
type cbTicker struct {
	ProductID string `json:"product_id"`
	Price     string `json:"price"`
	Volume24h string `json:"volume_24_h"`
}

func (c *Coinbase) readLoop(ctx context.Context, conn *websocket.Conn) error {
	for {
		if ctx.Err() != nil {
			return nil
		}
		conn.SetReadDeadline(time.Now().Add(60 * time.Second))
		_, msg, err := conn.ReadMessage()
		if err != nil {
			return err
		}

		c.mu.Lock()
		c.lastSeen = time.Now()
		c.mu.Unlock()

		var m cbMsg
		if err := json.Unmarshal(msg, &m); err != nil {
			continue
		}
		if m.Channel != "ticker" {
			continue
		}

		for _, ev := range m.Events {
			for _, t := range ev.Tickers {
				if !strings.HasSuffix(t.ProductID, "-USD") {
					continue
				}
				sym := strings.TrimSuffix(t.ProductID, "-USD")
				price, _ := strconv.ParseFloat(t.Price, 64)
				vol, _ := strconv.ParseFloat(t.Volume24h, 64)
				if price <= 0 {
					continue
				}
				select {
				case c.out <- Ticker{Exchange: "coinbase", Symbol: sym, Price: price, Volume24h: vol * price, Timestamp: time.Now()}:
				default:
				}
			}
		}
	}
}

func (c *Coinbase) setState(s Status, e string) {
	c.mu.Lock()
	c.status = s
	if e != "" {
		c.lastErr = e
	}
	c.mu.Unlock()
}

var _ Exchange = (*Coinbase)(nil)
