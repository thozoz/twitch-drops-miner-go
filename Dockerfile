# Stage 1: Build static Go binary
FROM golang:alpine AS builder

WORKDIR /build

# Cache dependency layer
COPY go.mod go.sum ./
RUN go mod download

# Copy source tree
COPY . .

# Build static binary with version ldflags
ARG VERSION=v1.0.0
ARG COMMIT=docker
ARG DATE=unknown

RUN CGO_ENABLED=0 GOOS=linux go build \
    -ldflags "-s -w -X github.com/thozoz/twitch-drops-miner-go/pkg/version.Version=${VERSION} -X github.com/thozoz/twitch-drops-miner-go/pkg/version.Commit=${COMMIT} -X github.com/thozoz/twitch-drops-miner-go/pkg/version.Date=${DATE}" \
    -o /build/tdm \
    ./cmd/tdm

# Stage 2: Minimal runtime container
FROM alpine:latest

RUN apk add --no-cache ca-certificates tzdata

# Create standard XDG persistent storage directories
RUN mkdir -p /root/.local/state/tdm /root/.config/tdm

COPY --from=builder /build/tdm /usr/local/bin/tdm

WORKDIR /root

ENTRYPOINT ["tdm"]
CMD ["run", "--daemon-mode"]
