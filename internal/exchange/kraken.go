package exchange

import (
	"encoding/json"
	"strings"
)

func NewKraken() *client {
	return newClient("kraken", "wss://ws.kraken.com/v2", 0, func(syms []string) any {
		ks := make([]string, len(syms))
		for i, s := range syms {
			ks[i] = s + "/USD"
		}
		return struct {
			Method string `json:"method"`
			Params struct {
				Channel string   `json:"channel"`
				Symbol  []string `json:"symbol"`
			} `json:"params"`
		}{Method: "subscribe", Params: struct {
			Channel string   `json:"channel"`
			Symbol  []string `json:"symbol"`
		}{Channel: "ticker", Symbol: ks}}
	}, parseKraken)
}

type krakenMsg struct {
	Channel string `json:"channel"`
	Data    []struct {
		Symbol string  `json:"symbol"`
		Last   float64 `json:"last"`
		Volume float64 `json:"volume"`
		Vwap   float64 `json:"vwap"`
	} `json:"data"`
}

func parseKraken(msg []byte) []Ticker {
	var m krakenMsg
	if json.Unmarshal(msg, &m) != nil || m.Channel != "ticker" {
		return nil
	}
	var out []Ticker
	for _, d := range m.Data {
		if !strings.HasSuffix(d.Symbol, "/USD") {
			continue
		}
		// Volume is base volume, so USD volume is volume * vwap.
		out = append(out, Ticker{Symbol: strings.TrimSuffix(d.Symbol, "/USD"), Price: d.Last, Volume24h: d.Volume * d.Vwap})
	}
	return out
}
