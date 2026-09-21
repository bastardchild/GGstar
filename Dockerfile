# syntax=docker/dockerfile:1

# ---------- stage 1: build ----------
FROM golang:1.25-alpine AS builder

WORKDIR /src

RUN apk add --no-cache git ca-certificates

COPY go.mod go.sum ./
RUN go mod download

COPY . .

ENV CGO_ENABLED=0 GOOS=linux
RUN go build -trimpath -ldflags="-s -w" -o /out/ggstar .

# Generate the 100 curated Sprouts avatars (DiceBear, CC0 1.0).
# Kept in this stage so the DiceBear style JSON never reaches the runtime image.
RUN cd tools/genavatars && go mod download && go run . -out /src/public/avatars \
    && test "$(ls /src/public/avatars/*.svg | wc -l)" = "100"

# ---------- stage 2: runtime ----------
FROM alpine:3.20

RUN apk add --no-cache ca-certificates tzdata wget \
    && adduser -D -u 10001 ggstar \
    && mkdir -p /data && chown -R ggstar:ggstar /data

WORKDIR /app

COPY --from=builder /out/ggstar /app/ggstar
COPY --from=builder /src/views /app/views
COPY --from=builder /src/config /app/config
COPY --from=builder /src/public /app/public

ENV PORT=3000 \
    DB_PATH=/data/ggstar.db

VOLUME ["/data"]
EXPOSE 3000

USER ggstar

HEALTHCHECK --interval=30s --timeout=5s --start-period=10s --retries=3 \
    CMD wget -qO- http://127.0.0.1:${PORT}/healthz || exit 1

CMD ["/app/ggstar"]
