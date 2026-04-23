# ── build stage ──────────────────────────────────────────────────────────────
FROM golang:1.22-alpine AS builder

WORKDIR /src

# Copy module files first; go mod tidy regenerates go.sum if needed.
COPY go.mod ./
# go.sum is optional here — tidy will create/update it.
COPY go.sum* ./

RUN go mod download 2>/dev/null; true

# Copy full source, then ensure go.sum is up-to-date before compiling.
COPY . .
RUN go mod tidy && \
    CGO_ENABLED=0 GOOS=linux go build -ldflags="-s -w" -o /cryptoapi ./cmd/api

# ── runtime stage ─────────────────────────────────────────────────────────────
FROM scratch

COPY --from=builder /cryptoapi /cryptoapi
COPY --from=builder /etc/ssl/certs/ca-certificates.crt /etc/ssl/certs/

# Default port; override at runtime with -e ADDR=":PORT" or -port flag
EXPOSE 8080

ENTRYPOINT ["/cryptoapi"]
