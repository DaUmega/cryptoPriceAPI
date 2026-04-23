package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/signal"
	"sync"
	"syscall"
	"time"

	"cryptoapi/internal/aggregator"
	"cryptoapi/internal/exchange"
	"cryptoapi/internal/server"
)

type manager struct {
	exchanges []exchange.Exchange
}

func (m *manager) Infos() []exchange.Info {
	out := make([]exchange.Info, len(m.exchanges))
	for i, ex := range m.exchanges {
		out[i] = ex.Info()
	}
	return out
}

func (m *manager) Subscribe(symbol string) {
	for _, ex := range m.exchanges {
		ex.Subscribe(symbol) //nolint:errcheck
	}
}

func (m *manager) ConnectedCount() int {
	n := 0
	for _, ex := range m.exchanges {
		if ex.Info().Status == "connected" {
			n++
		}
	}
	return n
}

func main() {
	port := flag.Int("port", 0, "port to listen on (default 8080, overrides ADDR env var)")
	flag.Parse()

	addr := os.Getenv("ADDR")
	switch {
	case *port != 0:
		addr = fmt.Sprintf(":%d", *port)
	case addr == "":
		addr = ":8080"
	}

	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()

	agg := aggregator.New()

	exchanges := []exchange.Exchange{
		exchange.NewBinance(),
		exchange.NewKraken(),
		exchange.NewCoinbase(),
		exchange.NewBybit(),
		exchange.NewOKX(),
		exchange.NewKuCoin(),
	}

	mgr := &manager{exchanges: exchanges}

	var wg sync.WaitGroup
	for _, ex := range exchanges {
		wg.Add(1)
		go func(ex exchange.Exchange) {
			defer wg.Done()
			if err := ex.Start(ctx); err != nil {
				log.Printf("[%s] start error: %v", ex.Name(), err)
			}
		}(ex)
	}

	for _, ex := range exchanges {
		go fanIn(ctx, ex.Tickers(), agg)
	}

	srv := server.New(agg, mgr)
	httpSrv := &http.Server{
		Addr:         addr,
		Handler:      srv.Handler(),
		ReadTimeout:  10 * time.Second,
		WriteTimeout: 30 * time.Second,
		IdleTimeout:  120 * time.Second,
	}

	go func() {
		log.Printf("listening on %s", addr)
		if err := httpSrv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Fatalf("http: %v", err)
		}
	}()

	<-ctx.Done()
	log.Println("shutting down...")

	shutCtx, shutCancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer shutCancel()
	httpSrv.Shutdown(shutCtx) //nolint:errcheck

	wg.Wait()
	log.Println("stopped")
}

func fanIn(ctx context.Context, ch <-chan exchange.Ticker, agg *aggregator.Aggregator) {
	for {
		select {
		case <-ctx.Done():
			return
		case t, ok := <-ch:
			if !ok {
				return
			}
			agg.Ingest(t)
		}
	}
}
