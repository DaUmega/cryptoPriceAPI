package exchange

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/gorilla/websocket"
)

func NewKuCoin() *client {
	var pingInterval time.Duration
	c := newClient("kucoin", "", 50, func(syms []string) any {
		ids := make([]string, len(syms))
		for i, s := range syms {
			ids[i] = s + "-USDT"
		}
		return struct {
			ID    string `json:"id"`
			Type  string `json:"type"`
			Topic string `json:"topic,omitempty"`
		}{ID: fmt.Sprintf("%d", time.Now().UnixMilli()), Type: "subscribe", Topic: "/market/snapshot:" + strings.Join(ids, ",")}
	}, parseKuCoin)
	c.dial = func(ctx context.Context) (*websocket.Conn, error) {
		wsURL, pi, err := kucoinToken(ctx)
		if err != nil {
			return nil, err
		}
		pingInterval = pi
		conn, _, err := websocket.DefaultDialer.DialContext(ctx, wsURL, nil)
		return conn, err
	}
	c.preRead = func(conn *websocket.Conn) error {
		// KuCoin sends a welcome frame immediately after connect; discard it.
		conn.SetReadDeadline(time.Now().Add(10 * time.Second))
		_, _, err := conn.ReadMessage()
		if err != nil {
			return fmt.Errorf("welcome: %w", err)
		}
		conn.SetReadDeadline(time.Time{})
		return nil
	}
	c.ping = func() (any, time.Duration) {
		return struct {
			ID   string `json:"id"`
			Type string `json:"type"`
		}{ID: fmt.Sprintf("%d", time.Now().UnixMilli()), Type: "ping"}, pingInterval
	}
	c.hook = func(conn *websocket.Conn, msg []byte) bool {
		var in struct {
			ID   string `json:"id"`
			Type string `json:"type"`
		}
		if json.Unmarshal(msg, &in) != nil || in.Type != "ping" {
			return false
		}
		_ = conn.WriteJSON(struct {
			ID   string `json:"id"`
			Type string `json:"type"`
		}{ID: in.ID, Type: "pong"})
		return true
	}
	return c
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

func kucoinToken(ctx context.Context) (string, time.Duration, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, "https://api.kucoin.com/api/v1/bullet-public", http.NoBody)
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
	return fmt.Sprintf("%s?token=%s&connectId=%d", srv.Endpoint, tr.Data.Token, time.Now().UnixMilli()), pi, nil
}

type kucoinIn struct {
	Type  string          `json:"type"`
	Topic string          `json:"topic"`
	Data  json.RawMessage `json:"data"`
}

// KuCoin snapshot payload is nested twice: outer data holds sequence
// plus an inner "data" object with the ticker itself. That nesting is real,
// verified live 2026-10-29, so the parser must keep it.
type kucoinSnap struct {
	Data struct {
		Symbol          string  `json:"symbol"`
		LastTradedPrice float64 `json:"lastTradedPrice"`
		VolValue        float64 `json:"volValue"`
		Vol             float64 `json:"vol"`
	} `json:"data"`
}

func parseKuCoin(msg []byte) []Ticker {
	var in kucoinIn
	if json.Unmarshal(msg, &in) != nil || in.Type != "message" || !strings.HasPrefix(in.Topic, "/market/snapshot:") {
		return nil
	}
	var s kucoinSnap
	if json.Unmarshal(in.Data, &s) != nil {
		return nil
	}
	if !strings.HasSuffix(s.Data.Symbol, "-USDT") {
		return nil
	}
	vol := s.Data.VolValue
	if vol <= 0 && s.Data.Vol > 0 {
		vol = s.Data.Vol * s.Data.LastTradedPrice
	}
	return []Ticker{{Symbol: strings.TrimSuffix(s.Data.Symbol, "-USDT"), Price: s.Data.LastTradedPrice, Volume24h: vol}}
}
