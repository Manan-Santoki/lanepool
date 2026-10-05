# syntax=docker/dockerfile:1

# --- dashboard -----------------------------------------------------------------
FROM --platform=$BUILDPLATFORM node:24-alpine AS web
WORKDIR /src/web
COPY web/package.json web/package-lock.json ./
RUN npm ci --no-audit --no-fund
COPY web/ ./
RUN npm run build

# --- Go binary -------------------------------------------------------------------
FROM --platform=$BUILDPLATFORM golang:1.27-alpine AS build
ARG TARGETOS TARGETARCH VERSION=dev
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
COPY --from=web /src/web/dist ./web/dist
RUN CGO_ENABLED=0 GOOS=$TARGETOS GOARCH=$TARGETARCH \
    go build -trimpath -ldflags "-s -w -X main.version=$VERSION" -o /out/lanepool ./cmd/lanepool

# --- runtime ---------------------------------------------------------------------
FROM alpine:3.22
RUN apk add --no-cache ca-certificates tzdata \
 && adduser -D -H -u 10001 lanepool \
 && mkdir -p /data && chown lanepool /data
# /data holds the lanes that worked (LANES_STATE_FILE); mount a volume there.
COPY --from=build /out/lanepool /usr/local/bin/lanepool
USER lanepool
LABEL org.opencontainers.image.title="lanepool" \
      org.opencontainers.image.description="Self-hosted rotating proxy over many WireGuard VPN exits" \
      org.opencontainers.image.source="https://github.com/Manan-Santoki/lanepool" \
      org.opencontainers.image.licenses="MIT"
EXPOSE 8000 8080 9090 9191
ENTRYPOINT ["lanepool"]
CMD ["all"]
