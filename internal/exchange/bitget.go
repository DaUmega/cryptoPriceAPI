package exchange

import (
	"encoding/json"
	"strconv"
	"strings"
	"time"
)

func NewBitget() *client {
	c := newClient("bitget", "wss://ws.bitget.com/v2/ws/public", 0, func(syms []string) any {
		args := make([]struct {
			InstType string `json:"instType"`
			Channel  string `json:"channel"`
			InstID   string `json:"instId"`
		}, len(syms))
		for i, s := range syms {
			args[i] = struct {
				InstType string `json:"instType"`
				Channel  string `json:"channel"`
				InstID   string `json:"instId"`
			}{InstType: "SPOT", Channel: "ticker", InstID: s + "USDT"}
		}
		return struct {
			Op   string `json:"op"`
			Args any    `json:"args"`
		}{Op: "subscribe", Args: args}
	}, parseBitget)
	c.ping = func() (any, time.Duration) {
		return map[string]string{"op": "ping"}, 25 * time.Second
	}
	return c
}

type bitgetMsg struct {
	Arg struct {
		Channel string `json:"channel"`
		InstID  string `json:"instId"`
	} `json:"arg"`
	Data []struct {
		InstID      string `json:"instId"`
		LastPr      string `json:"lastPr"`
		QuoteVolume string `json:"quoteVolume"`
	} `json:"data"`
}

func parseBitget(msg []byte) []Ticker {
	var m bitgetMsg
	if json.Unmarshal(msg, &m) != nil || m.Arg.Channel != "ticker" {
		return nil
	}
	var out []Ticker
	for _, d := range m.Data {
		if !strings.HasSuffix(d.InstID, "USDT") {
			continue
		}
		price, _ := strconv.ParseFloat(d.LastPr, 64)
		vol, _ := strconv.ParseFloat(d.QuoteVolume, 64)
		out = append(out, Ticker{Symbol: strings.TrimSuffix(d.InstID, "USDT"), Price: price, Volume24h: vol})
	}
	return out
}
