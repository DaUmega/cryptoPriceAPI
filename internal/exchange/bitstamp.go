package exchange

import (
	"encoding/json"
	"strings"
)

// Bitstamp has no ticker channel, only live_trades. A trade gives price but
// no 24h volume, so Volume24h is 0 and the aggregator weights it at 1. That
// is deliberate: one trade must not move a VWAP, but the price still counts
// as a source when other venues are absent.
func NewBitstamp() *client {
	return newClient("bitstamp", "wss://ws.bitstamp.net", 1, func(syms []string) any {
		return struct {
			Event string `json:"event"`
			Data  struct {
				Channel string `json:"channel"`
			} `json:"data"`
		}{Event: "bts:subscribe", Data: struct {
			Channel string `json:"channel"`
		}{Channel: "live_trades_" + strings.ToLower(syms[0]) + "usd"}}
	}, parseBitstamp)
}

type bitstampTrade struct {
	Event   string `json:"event"`
	Channel string `json:"channel"`
	Data    struct {
		Price float64 `json:"price"`
	} `json:"data"`
}

func parseBitstamp(msg []byte) []Ticker {
	var m bitstampTrade
	// Live trades arrive with event "trade", not "data", and price is a
	// JSON number, not a string. Both were wrong in the first version.
	if json.Unmarshal(msg, &m) != nil || m.Event != "trade" {
		return nil
	}
	if !strings.HasPrefix(m.Channel, "live_trades_") || !strings.HasSuffix(m.Channel, "usd") {
		return nil
	}
	sym := strings.ToUpper(strings.TrimSuffix(strings.TrimPrefix(m.Channel, "live_trades_"), "usd"))
	return []Ticker{{Symbol: sym, Price: m.Data.Price}}
}
