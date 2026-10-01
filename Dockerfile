# syntax=docker/dockerfile:1

FROM --platform=$BUILDPLATFORM alpine:3.20 AS binaries
ARG TARGETARCH
ARG WIREPROXY_VERSION=v1.1.3
ARG GLIDER_VERSION=0.16.4
RUN apk add --no-cache curl
WORKDIR /dl
RUN set -eu; \
    case "$TARGETARCH" in \
      amd64) WP_SHA=e88c1d090740373fc606c1bafd81d9a5eadc642cce5667616e20e9d7a444f51c; \
             GL_SHA=2b0f42d581b21545e27d5641caeb18896795620f25217f1b5d9fe2f02c685ea4 ;; \
      arm64) WP_SHA=370e00bd2167960d1ecd1c3c1439715bbaa94a0a110a2040468670c9af6021b6; \
             GL_SHA=131af46690f56ef62c9b682f26fd4841ed4c1d2c53251b850ff1d513a8809e3b ;; \
      *) echo "unsupported architecture: $TARGETARCH" >&2; exit 1 ;; \
    esac; \
    curl -fsSLo wireproxy.tgz "https://github.com/pufferffish/wireproxy/releases/download/${WIREPROXY_VERSION}/wireproxy_linux_${TARGETARCH}.tar.gz"; \
    curl -fsSLo glider.tgz "https://github.com/nadoo/glider/releases/download/v${GLIDER_VERSION}/glider_${GLIDER_VERSION}_linux_${TARGETARCH}.tar.gz"; \
    echo "${WP_SHA}  wireproxy.tgz" | sha256sum -c -; \
    echo "${GL_SHA}  glider.tgz" | sha256sum -c -; \
    tar xzf wireproxy.tgz wireproxy; \
    tar xzf glider.tgz --strip-components=1 "glider_${GLIDER_VERSION}_linux_${TARGETARCH}/glider"; \
    chmod 0755 wireproxy glider

FROM python:3.12-slim
LABEL org.opencontainers.image.title="lanepool" \
      org.opencontainers.image.description="Many WireGuard VPN exits (e.g. Surfshark) behind one rotating proxy" \
      org.opencontainers.image.source="https://github.com/Manan-Santoki/lanepool" \
      org.opencontainers.image.licenses="MIT"

COPY --from=binaries /dl/wireproxy /dl/glider /usr/local/bin/
COPY lanepool /app/lanepool

RUN useradd --system --uid 10001 --home-dir /nonexistent --shell /usr/sbin/nologin lanepool \
 && mkdir -p /config/wireguard

ENV PYTHONUNBUFFERED=1 \
    PYTHONDONTWRITEBYTECODE=1 \
    PYTHONPATH=/app \
    RUN_DIR=/run/lanepool

USER lanepool
WORKDIR /app
EXPOSE 8080 8000
HEALTHCHECK --interval=30s --timeout=5s --start-period=30s --start-interval=2s --retries=3 \
  CMD python -c "import os,urllib.request; urllib.request.urlopen('http://127.0.0.1:%s/healthz' % os.environ.get('API_PORT','8000'), timeout=4)"
ENTRYPOINT ["python", "-m", "lanepool"]
