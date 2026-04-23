package exchange

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/gorilla/websocket"
)

var kucoinRESTURL = "https://api.kucoin.com"

type KuCoin struct {
	mu       sync.RWMutex
	writeMu  sync.Mutex
	status   Status
	lastErr  string
	lastSeen time.Time
	symbols  map[string]bool
	out      chan Ticker
	conn     *websocket.Conn
}

func NewKuCoin() *KuCoin {
	return &KuCoin{
		symbols: make(map[string]bool),
		out:     make(chan Ticker, 1000),
	}
}

func (k *KuCoin) Name() string           { return "kucoin" }
func (k *KuCoin) Tickers() <-chan Ticker { return k.out }

func (k *KuCoin) Info() Info {
	k.mu.RLock()
	defer k.mu.RUnlock()
	return Info{
		Name:      "kucoin",
		Status:    k.status.String(),
		LastError: k.lastErr,
		LastSeen:  k.lastSeen,
		Symbols:   len(k.symbols),
	}
}

func (k *KuCoin) toExchange(sym string) string { return sym + "-USDT" }

func (k *KuCoin) fromExchange(s string) (string, bool) {
	if !strings.HasSuffix(s, "-USDT") {
		return "", false
	}
	return strings.TrimSuffix(s, "-USDT"), true
}

func (k *KuCoin) Start(ctx context.Context) error {
	go k.run(ctx)
	return nil
}

func (k *KuCoin) Subscribe(symbol string) error {
	k.mu.Lock()
	k.symbols[symbol] = true
	conn := k.conn
	k.mu.Unlock()
	if conn != nil {
		return k.sendSubscribe(conn, []string{symbol})
	}
	return nil
}

type kucoinOut struct {
	ID    string `json:"id"`
	Type  string `json:"type"`
	Topic string `json:"topic,omitempty"`
}

func (k *KuCoin) sendSubscribe(conn *websocket.Conn, symbols []string) error {
	ids := make([]string, len(symbols))
	for i, s := range symbols {
		ids[i] = k.toExchange(s)
	}
	k.writeMu.Lock()
	defer k.writeMu.Unlock()
	return conn.WriteJSON(kucoinOut{
		ID:    fmt.Sprintf("%d", time.Now().UnixMilli()),
		Type:  "subscribe",
		Topic: "/market/snapshot:" + strings.Join(ids, ","),
	})
}

type kucoinTokenResp struct {
	Code string `json:"code"`
	Data struct {
		Token           string `json:"token"`
		InstanceServers []struct {
			Endpoint     string `json:"endpoint"`
			PingInterval int    `json:"pingInterval"`
		} `json:"instanceServers"`
	} `json:"data"`
}

func (k *KuCoin) getToken(ctx context.Context) (wsURL string, pingInterval time.Duration, err error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost,
		kucoinRESTURL+"/api/v1/bullet-public", http.NoBody)
	if err != nil {
		return "", 0, err
	}
	resp, err := (&http.Client{Timeout: 10 * time.Second}).Do(req)
	if err != nil {
		return "", 0, err
	}
	defer resp.Body.Close()

	var tr kucoinTokenResp
	if err := json.NewDecoder(resp.Body).Decode(&tr); err != nil {
		return "", 0, err
	}
	if tr.Code != "200000" || len(tr.Data.InstanceServers) == 0 {
		return "", 0, fmt.Errorf("kucoin: bad token response (code %s)", tr.Code)
	}

	srv := tr.Data.InstanceServers[0]
	pi := time.Duration(srv.PingInterval) * time.Millisecond
	if pi <= 0 {
		pi = 18 * time.Second
	}
	url := fmt.Sprintf("%s?token=%s&connectId=%d", srv.Endpoint, tr.Data.Token, time.Now().UnixMilli())
	return url, pi, nil
}

func (k *KuCoin) run(ctx context.Context) {
	backoff := time.Second
	for {
		if ctx.Err() != nil {
			return
		}
		k.setState(Connecting, "")

		wsURL, pingInterval, err := k.getToken(ctx)
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

		conn, _, err := websocket.DefaultDialer.DialContext(ctx, wsURL, nil)
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

		// KuCoin sends a welcome frame immediately after connect; discard it.
		conn.SetReadDeadline(time.Now().Add(10 * time.Second))
		if _, _, err := conn.ReadMessage(); err != nil {
			conn.Close()
			k.setState(Err, "welcome: "+err.Error())
			continue
		}
		conn.SetReadDeadline(time.Time{})

		k.mu.Lock()
		k.conn = conn
		syms := make([]string, 0, len(k.symbols))
		for s := range k.symbols {
			syms = append(syms, s)
		}
		k.mu.Unlock()
		k.setState(Connected, "")
		log.Printf("[kucoin] connected; subscribing to %d symbol(s)", len(syms))

		for i := 0; i < len(syms); i += 50 {
			end := i + 50
			if end > len(syms) {
				end = len(syms)
			}
			if err := k.sendSubscribe(conn, syms[i:end]); err != nil {
				break
			}
		}

		go k.pingLoop(ctx, conn, pingInterval)

		if err := k.readLoop(ctx, conn); err != nil && ctx.Err() == nil {
			k.setState(Err, err.Error())
		}
		conn.Close()
		k.mu.Lock()
		k.conn = nil
		k.mu.Unlock()
	}
}

func (k *KuCoin) pingLoop(ctx context.Context, conn *websocket.Conn, interval time.Duration) {
	t := time.NewTicker(interval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			k.writeMu.Lock()
			err := conn.WriteJSON(kucoinOut{
				ID:   fmt.Sprintf("%d", time.Now().UnixMilli()),
				Type: "ping",
			})
			k.writeMu.Unlock()
			if err != nil {
				return
			}
		}
	}
}

type kucoinIn struct {
	ID    string          `json:"id"`
	Type  string          `json:"type"`
	Topic string          `json:"topic"`
	Data  json.RawMessage `json:"data"`
}

type kucoinSnap struct {
	Data struct {
		Symbol          string  `json:"symbol"`
		LastTradedPrice float64 `json:"lastTradedPrice"`
		VolValue        float64 `json:"volValue"`
	} `json:"data"`
}

func (k *KuCoin) readLoop(ctx context.Context, conn *websocket.Conn) error {
	for {
		if ctx.Err() != nil {
			return nil
		}
		conn.SetReadDeadline(time.Now().Add(40 * time.Second))
		_, raw, err := conn.ReadMessage()
		if err != nil {
			return err
		}

		k.mu.Lock()
		k.lastSeen = time.Now()
		k.mu.Unlock()

		var in kucoinIn
		if err := json.Unmarshal(raw, &in); err != nil {
			continue
		}

		if in.Type == "ping" {
			k.writeMu.Lock()
			conn.WriteJSON(kucoinOut{ID: in.ID, Type: "pong"}) //nolint:errcheck
			k.writeMu.Unlock()
			continue
		}
		if in.Type != "message" || !strings.HasPrefix(in.Topic, "/market/snapshot:") {
			continue
		}

		var snap kucoinSnap
		if err := json.Unmarshal(in.Data, &snap); err != nil {
			continue
		}

		sym, ok := k.fromExchange(snap.Data.Symbol)
		if !ok {
			continue
		}
		price := snap.Data.LastTradedPrice
		vol := snap.Data.VolValue
		if price <= 0 {
			continue
		}
		select {
		case k.out <- Ticker{Exchange: "kucoin", Symbol: sym, Price: price, Volume24h: vol, Timestamp: time.Now()}:
		default:
		}
	}
}

func (k *KuCoin) setState(s Status, e string) {
	k.mu.Lock()
	k.status = s
	if e != "" {
		k.lastErr = e
	}
	k.mu.Unlock()
}

var _ Exchange = (*KuCoin)(nil)
