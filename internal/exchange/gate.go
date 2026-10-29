package exchange

import (
	"encoding/json"
	"strconv"
	"strings"
	"time"
)

func NewGate() *client {
	c := newClient("gate", "wss://api.gateio.ws/ws/v4/", 0, func(syms []string) any {
		pairs := make([]string, len(syms))
		for i, s := range syms {
			pairs[i] = s + "_USDT"
		}
		return struct {
			Time    int64    `json:"time"`
			Channel string   `json:"channel"`
			Event   string   `json:"event"`
			Payload []string `json:"payload"`
		}{Time: time.Now().Unix(), Channel: "spot.tickers", Event: "subscribe", Payload: pairs}
	}, parseGate)
	return c
}

type gateMsg struct {
	Channel string `json:"channel"`
	Event   string `json:"event"`
	Result  struct {
		CurrencyPair string `json:"currency_pair"`
		Last         string `json:"last"`
		QuoteVolume  string `json:"quote_volume"`
	} `json:"result"`
}

func parseGate(msg []byte) []Ticker {
	var m gateMsg
	if json.Unmarshal(msg, &m) != nil || m.Channel != "spot.tickers" || m.Event != "update" {
		return nil
	}
	if !strings.HasSuffix(m.Result.CurrencyPair, "_USDT") {
		return nil
	}
	price, _ := strconv.ParseFloat(m.Result.Last, 64)
	vol, _ := strconv.ParseFloat(m.Result.QuoteVolume, 64)
	return []Ticker{{Symbol: strings.TrimSuffix(m.Result.CurrencyPair, "_USDT"), Price: price, Volume24h: vol}}
}
