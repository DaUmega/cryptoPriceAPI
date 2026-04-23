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

const okxWSURL = "wss://ws.okx.com:8443/ws/v5/public"

type OKX struct {
	mu       sync.RWMutex
	writeMu  sync.Mutex
	status   Status
	lastErr  string
	lastSeen time.Time
	symbols  map[string]bool
	out      chan Ticker
	conn     *websocket.Conn
}

func NewOKX() *OKX {
	return &OKX{
		symbols: make(map[string]bool),
		out:     make(chan Ticker, 1000),
	}
}

func (o *OKX) Name() string           { return "okx" }
func (o *OKX) Tickers() <-chan Ticker { return o.out }

func (o *OKX) Info() Info {
	o.mu.RLock()
	defer o.mu.RUnlock()
	return Info{
		Name:      "okx",
		Status:    o.status.String(),
		LastError: o.lastErr,
		LastSeen:  o.lastSeen,
		Symbols:   len(o.symbols),
	}
}

func (o *OKX) toExchange(sym string) string { return sym + "-USDT" }

func (o *OKX) fromExchange(s string) (string, bool) {
	if !strings.HasSuffix(s, "-USDT") {
		return "", false
	}
	return strings.TrimSuffix(s, "-USDT"), true
}

func (o *OKX) Start(ctx context.Context) error {
	go o.run(ctx)
	return nil
}

func (o *OKX) Subscribe(symbol string) error {
	o.mu.Lock()
	o.symbols[symbol] = true
	conn := o.conn
	o.mu.Unlock()
	if conn != nil {
		return o.sendSubscribe(conn, []string{symbol})
	}
	return nil
}

type okxSubMsg struct {
	Op   string   `json:"op"`
	Args []okxArg `json:"args"`
}
type okxArg struct {
	Channel string `json:"channel"`
	InstID  string `json:"instId"`
}

func (o *OKX) sendSubscribe(conn *websocket.Conn, symbols []string) error {
	args := make([]okxArg, len(symbols))
	for i, s := range symbols {
		args[i] = okxArg{Channel: "tickers", InstID: o.toExchange(s)}
	}
	o.writeMu.Lock()
	defer o.writeMu.Unlock()
	return conn.WriteJSON(okxSubMsg{Op: "subscribe", Args: args})
}

func (o *OKX) run(ctx context.Context) {
	backoff := time.Second
	for {
		if ctx.Err() != nil {
			return
		}
		o.setState(Connecting, "")

		conn, resp, err := websocket.DefaultDialer.DialContext(ctx, okxWSURL, nil)
		if err != nil {
			if resp != nil {
				log.Printf("[okx] dial failed: HTTP %d %s", resp.StatusCode, resp.Status)
			}
			o.setState(Err, err.Error())
			select {
			case <-ctx.Done():
				return
			case <-time.After(backoff):
				backoff = clampBackoff(backoff*2, 5*time.Minute)
			}
			continue
		}
		backoff = time.Second

		o.mu.Lock()
		o.conn = conn
		syms := make([]string, 0, len(o.symbols))
		for s := range o.symbols {
			syms = append(syms, s)
		}
		o.mu.Unlock()
		o.setState(Connected, "")
		log.Printf("[okx] connected; subscribing to %d symbol(s)", len(syms))

		if len(syms) > 0 {
			for i := 0; i < len(syms); i += 10 {
				end := i + 10
				if end > len(syms) {
					end = len(syms)
				}
				if err := o.sendSubscribe(conn, syms[i:end]); err != nil {
					break
				}
			}
		}

		go o.pingLoop(ctx, conn)

		if err := o.readLoop(ctx, conn); err != nil && ctx.Err() == nil {
			o.setState(Err, err.Error())
		}
		conn.Close()
		o.mu.Lock()
		o.conn = nil
		o.mu.Unlock()
	}
}

func (o *OKX) pingLoop(ctx context.Context, conn *websocket.Conn) {
	t := time.NewTicker(25 * time.Second)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			o.writeMu.Lock()
			err := conn.WriteMessage(websocket.TextMessage, []byte("ping"))
			o.writeMu.Unlock()
			if err != nil {
				return
			}
		}
	}
}

type okxPush struct {
	Arg  okxArg    `json:"arg"`
	Data []okxTick `json:"data"`
}
type okxTick struct {
	InstID    string `json:"instId"`
	Last      string `json:"last"`
	VolCcy24h string `json:"volCcy24h"`
}

func (o *OKX) readLoop(ctx context.Context, conn *websocket.Conn) error {
	for {
		if ctx.Err() != nil {
			return nil
		}
		conn.SetReadDeadline(time.Now().Add(40 * time.Second))
		_, msg, err := conn.ReadMessage()
		if err != nil {
			return err
		}

		o.mu.Lock()
		o.lastSeen = time.Now()
		o.mu.Unlock()

		if string(msg) == "pong" {
			continue
		}

		var push okxPush
		if err := json.Unmarshal(msg, &push); err != nil {
			continue
		}
		if push.Arg.Channel != "tickers" {
			continue
		}

		for _, t := range push.Data {
			sym, ok := o.fromExchange(t.InstID)
			if !ok {
				continue
			}
			price, _ := strconv.ParseFloat(t.Last, 64)
			vol, _ := strconv.ParseFloat(t.VolCcy24h, 64)
			if price <= 0 {
				continue
			}
			select {
			case o.out <- Ticker{Exchange: "okx", Symbol: sym, Price: price, Volume24h: vol, Timestamp: time.Now()}:
			default:
			}
		}
	}
}

func (o *OKX) setState(s Status, e string) {
	o.mu.Lock()
	o.status = s
	if e != "" {
		o.lastErr = e
	}
	o.mu.Unlock()
}

var _ Exchange = (*OKX)(nil)
