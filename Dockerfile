# syntax=docker/dockerfile:1.7

# Builder
FROM golang:1.24-alpine AS builder
WORKDIR /src
RUN apk add --no-cache git ca-certificates tzdata
COPY go.mod go.sum ./
RUN go mod download
COPY . .
ARG VERSION=dev
RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w -X main.version=${VERSION}" -o /out/tm ./cmd/tm

# Runtime — distroless/static-debian12 nonroot (~5MB, no shell, no libc)
FROM gcr.io/distroless/static-debian12:nonroot
COPY --from=builder /out/tm /tm
COPY --from=builder /usr/share/zoneinfo /usr/share/zoneinfo
COPY --from=builder /etc/ssl/certs/ca-certificates.crt /etc/ssl/certs/ca-certificates.crt

# nonroot user (UID 65532) — data stored in /home/nonroot/.tokenmeter
USER nonroot:nonroot
WORKDIR /home/nonroot

ENTRYPOINT ["/tm"]
CMD ["daemon"]

VOLUME ["/home/nonroot/.tokenmeter"]

HEALTHCHECK --interval=30s --timeout=5s --start-period=10s --retries=3 \
  CMD ["/tm", "healthcheck"]

LABEL org.opencontainers.image.title="tm" \
      org.opencontainers.image.description="Local AI coding agent token usage dashboard" \
      org.opencontainers.image.licenses="MIT" \
      org.opencontainers.image.source="https://github.com/tt-a1i/tokenmeter"
