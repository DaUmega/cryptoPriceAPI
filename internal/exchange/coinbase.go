package exchange

import (
	"encoding/json"
	"strconv"
	"strings"
)

func NewCoinbase() *client {
	return newClient("coinbase", "wss://advanced-trade-ws.coinbase.com", 0, func(syms []string) any {
		products := make([]string, len(syms))
		for i, s := range syms {
			products[i] = s + "-USD"
		}
		return struct {
			Type       string   `json:"type"`
			Channel    string   `json:"channel"`
			ProductIDs []string `json:"product_ids"`
		}{Type: "subscribe", Channel: "ticker", ProductIDs: products}
	}, parseCoinbase)
}

type cbMsg struct {
	Channel string `json:"channel"`
	Events  []struct {
		Tickers []struct {
			ProductID string `json:"product_id"`
			Price     string `json:"price"`
			Volume24h string `json:"volume_24_h"`
		} `json:"tickers"`
	} `json:"events"`
}

func parseCoinbase(msg []byte) []Ticker {
	var m cbMsg
	if json.Unmarshal(msg, &m) != nil || m.Channel != "ticker" {
		return nil
	}
	var out []Ticker
	for _, ev := range m.Events {
		for _, t := range ev.Tickers {
			if !strings.HasSuffix(t.ProductID, "-USD") {
				continue
			}
			price, _ := strconv.ParseFloat(t.Price, 64)
			vol, _ := strconv.ParseFloat(t.Volume24h, 64)
			// volume_24_h is base volume, so USD volume is vol * price.
			out = append(out, Ticker{Symbol: strings.TrimSuffix(t.ProductID, "-USD"), Price: price, Volume24h: vol * price})
		}
	}
	return out
}
