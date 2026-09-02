# Build stage
FROM golang:alpine AS builder

WORKDIR /src

RUN apk add --no-cache ca-certificates

# Cache dependencies
COPY go.mod go.sum ./
RUN go mod download

# Copy source code
COPY . .

# Build statically-linked binary
RUN CGO_ENABLED=0 GOOS=linux go build \
    -trimpath \
    -ldflags="-w -s" \
    -o /bin/gateway \
    ./cmd/gateway

# Production runtime stage
FROM alpine:latest

RUN apk add --no-cache ca-certificates tzdata \
    && addgroup -g 10001 -S appgroup \
    && adduser -u 10001 -S appuser -G appgroup

WORKDIR /app

# Copy binary and baseline config template
COPY --from=builder /bin/gateway /app/gateway
COPY --from=builder /src/config.yaml /app/config.yaml

# Run as non-root user
USER 10001:10001

EXPOSE 8080

ENTRYPOINT ["/app/gateway"]
