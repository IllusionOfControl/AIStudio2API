# ==========================================
# Stage 1: Build Vue.js Frontend
# ==========================================
FROM node:26-bookworm-slim AS web-builder

WORKDIR /build/web

COPY web/package.json web/package-lock.json ./

RUN yarn install

COPY web/ ./

RUN yarn run build

# ==========================================
# Stage 2: Build Go Backend
# ==========================================
FROM golang:1.26-bookworm AS go-builder

WORKDIR /build

COPY go.mod go.sum ./
ENV GOPROXY=direct
RUN go mod download

COPY . .
COPY --from=web-builder /build/internal/webui/dist ./internal/webui/dist

RUN CGO_ENABLED=0 go build \
	-trimpath \
	-ldflags="-s -w" \
	-o /app/aistudio2api \
	./cmd/aistudio2api

# ==========================================
# Stage 3: Preinstall Camoufox
# ==========================================
FROM debian:bookworm-slim AS camoufox-downloader

ARG CAMOUFOX_VERSION=152.0.4-beta.29
ARG TARGETARCH

RUN apt-get update && apt-get install -y --no-install-recommends \
    ca-certificates \
    curl \
    unzip \
    && rm -rf /var/lib/apt/lists/*

RUN ARCH_SUFFIX="" && \
    if [ "$TARGETARCH" = "arm64" ]; then ARCH_SUFFIX="arm64"; else ARCH_SUFFIX="x86_64"; fi && \
    DOWNLOAD_URL="https://github.com/daijro/camoufox/releases/download/v${CAMOUFOX_VERSION}/camoufox-${CAMOUFOX_VERSION}-lin.${ARCH_SUFFIX}.zip" && \
    echo "Downloading Camoufox from: ${DOWNLOAD_URL}" && \
    curl -fL --progress-bar "$DOWNLOAD_URL" -o camoufox.zip && \
	mkdir -p /camoufox/extracted && \
    echo "Extracting Camoufox..." && \
    unzip -q camoufox.zip -d /camoufox/extracted && \
    rm camoufox.zip && \
    chmod -R 0755 /camoufox/extracted

# ==========================================
# Stage 4: Minimal Runtime Image
# ==========================================
FROM debian:bookworm-slim AS runtime

ENV DEBIAN_FRONTEND=noninteractive

RUN apt-get update && apt-get install -y --no-install-recommends \
     ca-certificates \
     tzdata \
     xvfb \
     curl \
     procps \
     libgtk-3-0 \
     libdbus-glib-1-2 \
     libxt6 \
     libx11-xcb1 \
     libasound2 \
     libnss3 \
     libxcomposite1 \
     libxdamage1 \
     libxfixes3 \
     libxrandr2 \
     libpci3 \
     libglib2.0-0 \
     fonts-liberation \
     fonts-noto-color-emoji \
     && rm -rf /var/lib/apt/lists/*

WORKDIR /app

COPY --from=go-builder /app/aistudio2api /app/aistudio2api

COPY --from=camoufox-downloader /camoufox/extracted /app/runtime/camoufox/

COPY docker-entrypoint.sh /app/docker-entrypoint.sh

RUN chmod +x /app/aistudio2api /app/docker-entrypoint.sh

RUN mkdir -p /app/auth

ENV LISTEN_ADDR=0.0.0.0:2048 \
    AISTUDIO_AUTH_STATES=/app/auth \
    DISPLAY=:99 \
    CAMOUFOX_PATH=/app/runtime/camoufox/camoufox-bin \
    XVFB_ENABLE=true

EXPOSE 2048

ENTRYPOINT ["/app/docker-entrypoint.sh"]
CMD ["/app/aistudio2api", "--open-ui=false"]
