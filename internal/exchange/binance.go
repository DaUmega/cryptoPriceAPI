package exchange

import (
	"encoding/json"
	"strconv"
	"strings"
)

func NewBinance() *client {
	return newClient("binance", "wss://stream.binance.us:9443/stream", 0, func(syms []string) any {
		params := make([]string, len(syms))
		for i, s := range syms {
			params[i] = strings.ToLower(s+"USDT") + "@ticker"
		}
		return struct {
			Method string   `json:"method"`
			Params []string `json:"params"`
			ID     int      `json:"id"`
		}{Method: "SUBSCRIBE", Params: params, ID: 1}
	}, parseBinance)
}

type binanceCombinedMsg struct {
	Stream string      `json:"stream"`
	Data   binanceTick `json:"data"`
}

// Binance ticker fields use mixed case. Go matches case-insensitively, so
// "C" (stats-window close ms, number) would collide with "c" (last price,
// string) and "Q" with "q" (quote volume). Absorber fields force exact routing.
type binanceTick struct {
	Symbol    string      `json:"s"`
	Close     string      `json:"c"`
	CloseTime json.Number `json:"C"`
	Quote     string      `json:"q"`
	LastQty   string      `json:"Q"`
}

func parseBinance(msg []byte) []Ticker {
	var m binanceCombinedMsg
	if json.Unmarshal(msg, &m) != nil || !strings.HasSuffix(m.Stream, "@ticker") {
		return nil
	}
	if !strings.HasSuffix(m.Data.Symbol, "USDT") {
		return nil
	}
	price, _ := strconv.ParseFloat(m.Data.Close, 64)
	vol, _ := strconv.ParseFloat(m.Data.Quote, 64)
	return []Ticker{{Symbol: strings.TrimSuffix(m.Data.Symbol, "USDT"), Price: price, Volume24h: vol}}
}
