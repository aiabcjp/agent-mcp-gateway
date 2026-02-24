# Build stage
FROM golang:1.24-alpine AS builder

RUN apk add --no-cache git ca-certificates

WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download

COPY . .
RUN CGO_ENABLED=0 GOOS=linux go build -trimpath \
    -ldflags "-s -w -X main.Version=$(git describe --tags --always --dirty 2>/dev/null || echo docker)" \
    -o /bin/qa-gateway ./cmd/qa-gateway

# Runtime stage
FROM alpine:3.20

RUN apk add --no-cache ca-certificates tzdata
RUN adduser -D -u 1000 gateway

COPY --from=builder /bin/qa-gateway /usr/local/bin/qa-gateway

USER gateway
WORKDIR /home/gateway

EXPOSE 443 80

ENTRYPOINT ["qa-gateway"]
CMD ["serve", "--config", "/etc/qa-gateway/config.yaml"]
