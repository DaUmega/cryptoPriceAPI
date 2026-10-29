package exchange

import (
	"encoding/json"
	"strconv"
	"strings"
	"time"
)

func NewBybit() *client {
	c := newClient("bybit", "wss://stream.bybit.com/v5/public/spot", 10, func(syms []string) any {
		args := make([]string, len(syms))
		for i, s := range syms {
			args[i] = "tickers." + s + "USDT"
		}
		return struct {
			Op   string   `json:"op"`
			Args []string `json:"args"`
		}{Op: "subscribe", Args: args}
	}, parseBybit)
	c.ping = func() (any, time.Duration) {
		return map[string]string{"op": "ping"}, 20 * time.Second
	}
	return c
}

type bybitMsg struct {
	Topic string `json:"topic"`
	Data  struct {
		Symbol      string `json:"symbol"`
		LastPrice   string `json:"lastPrice"`
		Turnover24h string `json:"turnover24h"`
	} `json:"data"`
}

func parseBybit(msg []byte) []Ticker {
	var m bybitMsg
	if json.Unmarshal(msg, &m) != nil || !strings.HasPrefix(m.Topic, "tickers.") {
		return nil
	}
	if !strings.HasSuffix(m.Data.Symbol, "USDT") {
		return nil
	}
	price, _ := strconv.ParseFloat(m.Data.LastPrice, 64)
	vol, _ := strconv.ParseFloat(m.Data.Turnover24h, 64)
	return []Ticker{{Symbol: strings.TrimSuffix(m.Data.Symbol, "USDT"), Price: price, Volume24h: vol}}
}
