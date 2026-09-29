# syntax=docker/dockerfile:1
# Multi-arch build: `docker buildx build --platform linux/amd64,linux/arm64 .`
# The frontend and Go stages run on the build host's native platform
# ($BUILDPLATFORM) and cross-compile for $TARGETARCH, so arm64 images don't
# pay the QEMU emulation cost for npm/go.

# Build stage for Frontend (architecture-independent output)
FROM --platform=$BUILDPLATFORM node:24-alpine AS frontend-builder
WORKDIR /app/frontend
COPY frontend/package*.json ./
RUN npm install
COPY frontend .
RUN npm run build

# Build stage for Backend (Go)
FROM --platform=$BUILDPLATFORM golang:1.25-alpine AS backend-builder
WORKDIR /app/backend
# Install dependencies needed for cgo if ever required, though we use pure go sqlite now
RUN apk add --no-cache gcc musl-dev
COPY backend/go.mod backend/go.sum ./
RUN go mod download
COPY backend .
# Sync freshly built frontend into the go:embed directory
COPY --from=frontend-builder /app/frontend/dist ./internal/api/dist/
# Disable CGO to ensure statically linked binary
ENV CGO_ENABLED=0
# Supplied by BuildKit — don't give them defaults here, a default would
# override the automatic value and always produce an amd64 binary.
ARG TARGETOS
ARG TARGETARCH
# Build version, injected at link time. CI passes the git tag (e.g. v1.0.0) or
# a "main-<sha>" string via the VERSION build-arg; defaults to "dev" locally.
ARG VERSION=dev
RUN GOOS=${TARGETOS:-linux} GOARCH=${TARGETARCH:-amd64} go build -ldflags "-s -w -X github.com/harveyxiacn/ZenithPanel/backend/internal/version.Version=${VERSION}" -o /zenithpanel main.go

# Final Runtime Image
FROM alpine:latest
WORKDIR /opt/zenithpanel

# Pinned proxy engine versions — update these when a new release is desired.
# Using ARGs avoids GitHub API rate-limit failures during CI builds.
ARG XRAY_VERSION=v26.2.6
ARG SINGBOX_VERSION=v1.14.2
# Set automatically by BuildKit to the platform being built (amd64 / arm64).
ARG TARGETARCH

# Install basic runtime dependencies (ca-certificates for TLS, tzdata, etc).
# iproute2 provides `ss`, used by the traffic-egress socket sampler to attribute
# host-wide outbound flows to proxy processes.
RUN apk add --no-cache ca-certificates tzdata sqlite-libs docker-cli bash iptables iproute2 util-linux unzip

# Download Xray-core binary + geodata
RUN set -ex && \
    ARCH="${TARGETARCH:-$(apk --print-arch)}" && \
    case "$ARCH" in \
      amd64|x86_64)  XRAY_ASSET=Xray-linux-64.zip ;; \
      arm64|aarch64) XRAY_ASSET=Xray-linux-arm64-v8a.zip ;; \
      *) echo "unsupported arch: $ARCH" >&2; exit 1 ;; \
    esac && \
    wget -O /tmp/xray.zip "https://github.com/XTLS/Xray-core/releases/download/${XRAY_VERSION}/${XRAY_ASSET}" && \
    unzip /tmp/xray.zip xray geoip.dat geosite.dat -d /usr/local/bin/ && \
    chmod +x /usr/local/bin/xray && \
    rm -f /tmp/xray.zip && \
    xray version

# Download Sing-box binary (required for Hysteria2, TUIC, and alternative engine)
RUN set -ex && \
    ARCH="${TARGETARCH:-$(apk --print-arch)}" && \
    case "$ARCH" in \
      amd64|x86_64)  SB_ARCH=amd64 ;; \
      arm64|aarch64) SB_ARCH=arm64 ;; \
      *) echo "unsupported arch: $ARCH" >&2; exit 1 ;; \
    esac && \
    SINGBOX_VER_STRIP="${SINGBOX_VERSION#v}" && \
    SB_BASE="https://github.com/SagerNet/sing-box/releases/download/${SINGBOX_VERSION}" && \
    # Since 1.12 the plain linux tarball is glibc-linked (exit 127 on Alpine);
    # use the -musl build, falling back to the plain one for older versions.
    SB_NAME="sing-box-${SINGBOX_VER_STRIP}-linux-${SB_ARCH}-musl" && \
    if ! wget -O /tmp/singbox.tar.gz "${SB_BASE}/${SB_NAME}.tar.gz"; then \
      SB_NAME="sing-box-${SINGBOX_VER_STRIP}-linux-${SB_ARCH}" && \
      wget -O /tmp/singbox.tar.gz "${SB_BASE}/${SB_NAME}.tar.gz"; \
    fi && \
    tar -xzf /tmp/singbox.tar.gz -C /tmp && \
    cp /tmp/${SB_NAME}/sing-box /usr/local/bin/sing-box && \
    chmod 755 /usr/local/bin/sing-box && \
    rm -rf /tmp/singbox.tar.gz /tmp/sing-box-* && \
    sing-box version

# Copy backend binary (frontend is already embedded via go:embed)
COPY --from=backend-builder /zenithpanel /opt/zenithpanel/zenithpanel

# Package vps_check.sh so diagnostics work in container deployments
COPY scripts/vps_check.sh /opt/zenithpanel/scripts/vps_check.sh
RUN chmod +x /opt/zenithpanel/scripts/vps_check.sh

# Ensure the database and logs directories exist
RUN mkdir -p /opt/zenithpanel/data /opt/zenithpanel/logs

# Environment variables
ENV GIN_MODE=release
ENV TZ=Asia/Shanghai

CMD ["/opt/zenithpanel/zenithpanel"]
