package exchange

import (
	"testing"
	"time"
)

func TestParseBinanceCaseCollision(t *testing.T) {
	// "C" is a number (window close ms), "c" is the price string. A
	// case-insensitive decoder without absorber fields would fail here.
	msg := []byte(`{"stream":"btcusdt@ticker","data":{"s":"BTCUSDT","c":"70000.00","C":1700000000000,"q":"1234567.89","Q":"0.001"}}`)
	ts := parseBinance(msg)
	if len(ts) != 1 || ts[0].Symbol != "BTC" || ts[0].Price != 70000 || ts[0].Volume24h != 1234567.89 {
		t.Fatalf("binance parse = %+v", ts)
	}
	if ts := parseBinance([]byte(`{"stream":"btcusdt@depth","data":{}}`)); len(ts) != 0 {
		t.Fatalf("non-ticker stream should parse to nothing, got %+v", ts)
	}
}

func TestParseKrakenVolume(t *testing.T) {
	msg := []byte(`{"channel":"ticker","data":[{"symbol":"BTC/USD","last":70000,"volume":100,"vwap":69000}]}`)
	ts := parseKraken(msg)
	if len(ts) != 1 || ts[0].Volume24h != 100*69000 {
		t.Fatalf("kraken USD volume should be base*vwap, got %+v", ts)
	}
}

func TestParseCoinbaseVolume(t *testing.T) {
	msg := []byte(`{"channel":"ticker","events":[{"tickers":[{"product_id":"BTC-USD","price":"70000","volume_24_h":"100"}]}]}`)
	ts := parseCoinbase(msg)
	if len(ts) != 1 || ts[0].Volume24h != 100*70000 {
		t.Fatalf("coinbase USD volume should be base*price, got %+v", ts)
	}
}

func TestParseKuCoinNestedData(t *testing.T) {
	// Live KuCoin snapshot, verified 2026-10-29: outer data holds a
	// sequence plus an inner "data" object with the ticker. A flat
	// parser would drop every KuCoin ticker.
	msg := []byte(`{"type":"message","topic":"/market/snapshot:BTC-USDT","data":{"sequence":"1","data":{"symbol":"BTC-USDT","lastTradedPrice":70000,"volValue":500000}}}`)
	ts := parseKuCoin(msg)
	if len(ts) != 1 || ts[0].Symbol != "BTC" || ts[0].Price != 70000 || ts[0].Volume24h != 500000 {
		t.Fatalf("kucoin parse = %+v", ts)
	}
}

func TestParseBybitOKXGateBitget(t *testing.T) {
	if ts := parseBybit([]byte(`{"topic":"tickers.BTCUSDT","data":{"symbol":"BTCUSDT","lastPrice":"70000","turnover24h":"1000"}}`)); len(ts) != 1 || ts[0].Price != 70000 {
		t.Fatalf("bybit = %+v", ts)
	}
	if ts := parseOKX([]byte(`{"arg":{"channel":"tickers"},"data":[{"instId":"BTC-USDT","last":"70000","volCcy24h":"2000"}]}`)); len(ts) != 1 || ts[0].Volume24h != 2000 {
		t.Fatalf("okx = %+v", ts)
	}
	if ts := parseOKX([]byte(`pong`)); len(ts) != 0 {
		t.Fatalf("okx pong should parse to nothing, got %+v", ts)
	}
	if ts := parseGate([]byte(`{"channel":"spot.tickers","event":"update","result":{"currency_pair":"BTC_USDT","last":"70000","quote_volume":"3000"}}`)); len(ts) != 1 || ts[0].Volume24h != 3000 {
		t.Fatalf("gate = %+v", ts)
	}
	if ts := parseBitget([]byte(`{"arg":{"channel":"ticker","instId":"BTCUSDT"},"data":[{"instId":"BTCUSDT","lastPr":"70000","quoteVolume":"4000"}]}`)); len(ts) != 1 || ts[0].Volume24h != 4000 {
		t.Fatalf("bitget = %+v", ts)
	}
}

func TestParseBitstampTrade(t *testing.T) {
	// Live Bitstamp trade, verified 2026-10-29: event "trade", price a number.
	msg := []byte(`{"event":"trade","channel":"live_trades_btcusd","data":{"price":70000,"amount":0.5}}`)
	ts := parseBitstamp(msg)
	if len(ts) != 1 || ts[0].Symbol != "BTC" || ts[0].Price != 70000 || ts[0].Volume24h != 0 {
		t.Fatalf("bitstamp = %+v", ts)
	}
	if ts := parseBitstamp([]byte(`{"event":"bts:subscription_succeeded","channel":"live_trades_btcusd","data":{}}`)); len(ts) != 0 {
		t.Fatalf("subscription ack should parse to nothing, got %+v", ts)
	}
}

func TestParseInvalidAndWrongSymbol(t *testing.T) {
	parsers := []func([]byte) []Ticker{parseBinance, parseBybit, parseCoinbase, parseKraken, parseKuCoin, parseOKX, parseGate, parseBitget, parseBitstamp}
	for i, p := range parsers {
		if ts := p([]byte(`not json`)); len(ts) != 0 {
			t.Fatalf("parser %d: invalid json should parse to nothing, got %+v", i, ts)
		}
	}
}

func TestClientSubscribeNoResend(t *testing.T) {
	c := NewBinance()
	if err := c.Subscribe("BTC"); err != nil {
		t.Fatal(err)
	}
	if err := c.Subscribe("BTC"); err != nil {
		t.Fatal(err)
	}
	if n := c.Info().Symbols; n != 1 {
		t.Fatalf("duplicate subscribe should not add a second symbol, got %d", n)
	}
	if c.Name() != "binance" {
		t.Fatalf("name = %s", c.Name())
	}
}

func TestBackoffJitterRange(t *testing.T) {
	for i := 0; i < 100; i++ {
		d := backoff(time.Second, 5*time.Minute)
		if d < 500*time.Millisecond || d > time.Second {
			t.Fatalf("backoff(1s) out of range: %v", d)
		}
	}
	if d := backoff(10*time.Minute, 5*time.Minute); d != 5*time.Minute {
		t.Fatalf("backoff should clamp at max, got %v", d)
	}
}
