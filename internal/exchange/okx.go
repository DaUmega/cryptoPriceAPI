package exchange

import (
	"encoding/json"
	"strconv"
	"strings"
	"time"
)

func NewOKX() *client {
	c := newClient("okx", "wss://ws.okx.com:8443/ws/v5/public", 10, func(syms []string) any {
		args := make([]struct {
			Channel string `json:"channel"`
			InstID  string `json:"instId"`
		}, len(syms))
		for i, s := range syms {
			args[i] = struct {
				Channel string `json:"channel"`
				InstID  string `json:"instId"`
			}{Channel: "tickers", InstID: s + "-USDT"}
		}
		return struct {
			Op   string `json:"op"`
			Args any    `json:"args"`
		}{Op: "subscribe", Args: args}
	}, parseOKX)
	c.ping = func() (any, time.Duration) { return "ping", 25 * time.Second }
	return c
}

type okxPush struct {
	Arg struct {
		Channel string `json:"channel"`
	} `json:"arg"`
	Data []struct {
		InstID    string `json:"instId"`
		Last      string `json:"last"`
		VolCcy24h string `json:"volCcy24h"`
	} `json:"data"`
}

func parseOKX(msg []byte) []Ticker {
	if string(msg) == "pong" {
		return nil
	}
	var m okxPush
	if json.Unmarshal(msg, &m) != nil || m.Arg.Channel != "tickers" {
		return nil
	}
	var out []Ticker
	for _, t := range m.Data {
		if !strings.HasSuffix(t.InstID, "-USDT") {
			continue
		}
		price, _ := strconv.ParseFloat(t.Last, 64)
		vol, _ := strconv.ParseFloat(t.VolCcy24h, 64)
		out = append(out, Ticker{Symbol: strings.TrimSuffix(t.InstID, "-USDT"), Price: price, Volume24h: vol})
	}
	return out
}
