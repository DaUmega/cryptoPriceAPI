package exchange

import (
	"context"
	"log"
	"sync"
	"time"

	"github.com/gorilla/websocket"
)

// client is the shared websocket runner for every venue. Each venue only
// supplies its URL, subscribe message, ticker parser, and optional app-level
// ping, pre-read, and message hook. That is what keeps the venue files short.
type client struct {
	name      string
	url       string
	batch     int // 0 = all symbols in one message, else symbols per message
	dial      func(ctx context.Context) (*websocket.Conn, error)
	subscribe func([]string) any
	parse     func([]byte) []Ticker
	ping      func() (any, time.Duration)                 // fresh message, interval; nil = no app ping
	hook      func(conn *websocket.Conn, msg []byte) bool // true = handled, skip parse
	preRead   func(conn *websocket.Conn) error

	mu       sync.RWMutex
	writeMu  sync.Mutex
	status   Status
	lastErr  string
	lastSeen time.Time
	symbols  map[string]bool
	out      chan Ticker
	conn     *websocket.Conn
}

func newClient(name, url string, batch int, sub func([]string) any, parse func([]byte) []Ticker) *client {
	return &client{name: name, url: url, batch: batch, subscribe: sub, parse: parse, symbols: make(map[string]bool), out: make(chan Ticker, 1000)}
}

func (c *client) Name() string           { return c.name }
func (c *client) Tickers() <-chan Ticker { return c.out }

func (c *client) Info() Info {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return Info{Name: c.name, Status: c.status.String(), LastError: c.lastErr, LastSeen: c.lastSeen, Symbols: len(c.symbols)}
}

func (c *client) Start(ctx context.Context) error {
	go c.run(ctx)
	return nil
}

// Subscribe is a no-op for a symbol already tracked, so a price request never
// resends a subscribe for a symbol this venue already serves.
func (c *client) Subscribe(symbol string) error {
	c.mu.Lock()
	if c.symbols[symbol] {
		c.mu.Unlock()
		return nil
	}
	c.symbols[symbol] = true
	conn := c.conn
	c.mu.Unlock()
	if conn == nil {
		return nil
	}
	return c.send(conn, []string{symbol})
}

func (c *client) write(conn *websocket.Conn, msg any) error {
	c.writeMu.Lock()
	defer c.writeMu.Unlock()
	if s, ok := msg.(string); ok {
		return conn.WriteMessage(websocket.TextMessage, []byte(s))
	}
	return conn.WriteJSON(msg)
}

func (c *client) send(conn *websocket.Conn, syms []string) error {
	return c.write(conn, c.subscribe(syms))
}

func (c *client) sendAll(conn *websocket.Conn, syms []string) error {
	if c.batch <= 0 {
		return c.send(conn, syms)
	}
	for i := 0; i < len(syms); i += c.batch {
		end := i + c.batch
		if end > len(syms) {
			end = len(syms)
		}
		if err := c.send(conn, syms[i:end]); err != nil {
			return err
		}
	}
	return nil
}

func (c *client) setState(s Status, e string) {
	c.mu.Lock()
	c.status = s
	if e != "" {
		c.lastErr = e
	}
	c.mu.Unlock()
}

func (c *client) run(ctx context.Context) {
	wait := time.Second
	for ctx.Err() == nil {
		c.setState(Connecting, "")
		var conn *websocket.Conn
		var err error
		if c.dial != nil {
			conn, err = c.dial(ctx)
		} else {
			conn, _, err = websocket.DefaultDialer.DialContext(ctx, c.url, nil)
		}
		if err != nil {
			c.setState(Err, err.Error())
			log.Printf("[%s] dial failed: %v", c.name, err)
			select {
			case <-ctx.Done():
				return
			case <-time.After(wait):
				wait = backoff(wait*2, 5*time.Minute)
			}
			continue
		}
		wait = time.Second

		// Extend the read deadline on every websocket ping, so a quiet
		// low-volume pair does not look dead just because no ticker arrived.
		conn.SetPingHandler(func(appData string) error {
			conn.SetReadDeadline(time.Now().Add(60 * time.Second)) //nolint:errcheck
			return conn.WriteControl(websocket.PongMessage, []byte(appData), time.Now().Add(5*time.Second))
		})

		if c.preRead != nil {
			if err := c.preRead(conn); err != nil {
				conn.Close()
				c.setState(Err, err.Error())
				continue
			}
		}

		c.mu.Lock()
		c.conn = conn
		syms := make([]string, 0, len(c.symbols))
		for s := range c.symbols {
			syms = append(syms, s)
		}
		c.mu.Unlock()
		c.setState(Connected, "")
		log.Printf("[%s] connected; subscribing to %d symbol(s)", c.name, len(syms))

		if len(syms) > 0 {
			if err := c.sendAll(conn, syms); err != nil {
				conn.Close()
				c.mu.Lock()
				c.conn = nil
				c.mu.Unlock()
				c.setState(Err, err.Error())
				continue
			}
		}

		// App-level ping lives only as long as this connection does, so a
		// reconnect never leaves a stale ping loop writing to a dead socket.
		connCtx, cancel := context.WithCancel(ctx)
		if c.ping != nil {
			if msg, interval := c.ping(); msg != nil && interval > 0 {
				go c.pingLoop(connCtx, conn, interval)
			}
		}
		err = c.readLoop(connCtx, conn)
		cancel()
		if err != nil && ctx.Err() == nil {
			c.setState(Err, err.Error())
			log.Printf("[%s] read error: %v", c.name, err)
		}
		conn.Close()
		c.mu.Lock()
		c.conn = nil
		c.mu.Unlock()
	}
}

func (c *client) pingLoop(ctx context.Context, conn *websocket.Conn, interval time.Duration) {
	t := time.NewTicker(interval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			msg, _ := c.ping()
			if msg == nil {
				return
			}
			if err := c.write(conn, msg); err != nil {
				return
			}
		}
	}
}

func (c *client) readLoop(ctx context.Context, conn *websocket.Conn) error {
	for ctx.Err() == nil {
		conn.SetReadDeadline(time.Now().Add(60 * time.Second))
		_, msg, err := conn.ReadMessage()
		if err != nil {
			return err
		}
		c.mu.Lock()
		c.lastSeen = time.Now()
		c.mu.Unlock()
		if c.hook != nil && c.hook(conn, msg) {
			continue
		}
		for _, t := range c.parse(msg) {
			if t.Price <= 0 {
				continue
			}
			c.mu.RLock()
			watched := c.symbols[t.Symbol]
			c.mu.RUnlock()
			if !watched {
				continue
			}
			t.Exchange = c.name
			t.Timestamp = time.Now()
			select {
			case c.out <- t:
			default:
			}
		}
	}
	return nil
}

var _ Exchange = (*client)(nil)
