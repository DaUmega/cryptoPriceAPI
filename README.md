# cryptoPriceAPI

Aggregates real-time crypto prices across 6 exchanges into a single REST API.

Pulls live ticker data from Binance, Kraken, Coinbase, Bybit, OKX, and KuCoin simultaneously and returns a consolidated price with per-exchange breakdown.

---

## Endpoints

| Method | Path | Description |
|--------|------|-------------|
| `GET` | `/health` | Liveness check |
| `GET` | `/status` | Exchange connection status and uptime |
| `GET` | `/price/{symbol}` | Aggregated price for a symbol (e.g. `BTC`, `ETH`) |

### Example

```
GET /price/BTC
```

```json
{
  "symbol": "BTC",
  "price_usd": 77688.3861618306,
  "volume_24h_usd": 4178565223.083602,
  "sources": [
    {
      "exchange": "coinbase",
      "price": 77707.71,
      "volume_24h_usd": 800366920.8203881,
      "updated_at": "2026-04-23T12:19:00.273935134Z"
    },
    {
      "exchange": "kraken",
      "price": 77701.2,
      "volume_24h_usd": 152867790.1792346,
      "updated_at": "2026-04-23T12:18:50.165704975Z"
    },
    {
      "exchange": "bybit",
      "price": 77681.8,
      "volume_24h_usd": 884880480.6451163,
      "updated_at": "2026-04-23T12:18:59.476492928Z"
    },
    {
      "exchange": "okx",
      "price": 77684.8,
      "volume_24h_usd": 576205871.246873,
      "updated_at": "2026-04-23T12:19:00.235546495Z"
    },
    {
      "exchange": "binance",
      "price": 77683.33,
      "volume_24h_usd": 1411442745.267198,
      "updated_at": "2026-04-23T12:19:00.118537276Z"
    },
    {
      "exchange": "kucoin",
      "price": 77681.6,
      "volume_24h_usd": 352801414.9247918,
      "updated_at": "2026-04-23T12:19:00.018815545Z"
    }
  ],
  "method": "vwap",
  "updated_at": "2026-04-23T12:19:00.31983981Z"
}
```

---

## Quick Start

Requires [Docker](https://docs.docker.com/get-docker/).

```bash
./manage.sh start
```

The API will be available at `http://localhost:8080`.

### manage.sh commands

```
build      Build the Docker image
start      Build (if needed) and start the container
stop       Stop the running container
restart    Stop and start the container
status     Show container state and exchange health
logs [-f]  Print recent logs (add -f to follow)
shell      Open a shell session inside the container
test       Run API endpoint tests
update     Rebuild the image and restart the container
purge      Stop and remove the container and image
```

---

## Configuration

| Variable | Description | Default |
|----------|-------------|---------|
| `CRYPTOAPI_PORT` | Host port to bind | `8080` |

```bash
CRYPTOAPI_PORT=9090 ./manage.sh start
```

### Running without Docker

```bash
go build -o cryptoapi ./cmd/api
./cryptoapi -port 9090
```

The `-port` flag takes precedence over the `ADDR` environment variable.

---

## Requirements

- Docker (for `manage.sh`)
- Go 1.22+ (for direct builds)
