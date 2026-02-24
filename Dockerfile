# Build stage
FROM golang:1.24-alpine AS builder

RUN apk add --no-cache git ca-certificates

WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download

COPY . .
RUN CGO_ENABLED=0 GOOS=linux go build -trimpath \
    -ldflags "-s -w -X main.Version=$(git describe --tags --always --dirty 2>/dev/null || echo docker)" \
    -o /bin/agent-mcp-gateway ./cmd/agent-mcp-gateway

# Runtime stage
FROM alpine:3.20

RUN apk add --no-cache ca-certificates tzdata
RUN adduser -D -u 1000 gateway

COPY --from=builder /bin/agent-mcp-gateway /usr/local/bin/agent-mcp-gateway

USER gateway
WORKDIR /home/gateway

EXPOSE 443 80

ENTRYPOINT ["agent-mcp-gateway"]
CMD ["serve", "--config", "/etc/agent-mcp-gateway/config.yaml"]
