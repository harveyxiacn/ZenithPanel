# syntax=docker/dockerfile:1
# Multi-arch build: `docker buildx build --platform linux/amd64,linux/arm64 .`
# The frontend and Go stages run on the build host's native platform
# ($BUILDPLATFORM) and cross-compile for $TARGETARCH, so arm64 images don't
# pay the QEMU emulation cost for npm/go.

# Build stage for Frontend (architecture-independent output)
FROM --platform=$BUILDPLATFORM node:26-alpine@sha256:40cd011b55415bc894e45b02fdd5a5dfde056921b7d23a64c74f80497af48797 AS frontend-builder
WORKDIR /app/frontend
COPY frontend/package*.json ./
RUN npm ci
COPY frontend .
RUN npm run build

# Build stage for Backend (Go)
FROM --platform=$BUILDPLATFORM golang:1.26.10-alpine@sha256:a2012b4a1d1f9f7e9f44e0d39b805a901e856b1dd80b4e6ee4338db547050308 AS backend-builder
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

# Build the pinned OTA signature verifier for the target architecture.
FROM --platform=$BUILDPLATFORM golang:1.26.10-alpine@sha256:a2012b4a1d1f9f7e9f44e0d39b805a901e856b1dd80b4e6ee4338db547050308 AS cosign-builder
ARG TARGETOS
ARG TARGETARCH
WORKDIR /src
RUN go mod init verifier && go get github.com/sigstore/cosign/v3/cmd/cosign@v3.0.6
RUN CGO_ENABLED=0 GOOS=${TARGETOS:-linux} GOARCH=${TARGETARCH:-amd64} go build -o /out/cosign github.com/sigstore/cosign/v3/cmd/cosign

# Final Runtime Image
FROM alpine:3.23@sha256:fd791d74b68913cbb027c6546007b3f0d3bc45125f797758156952bc2d6daf40
WORKDIR /opt/zenithpanel

# Pinned proxy engine versions — update these when a new release is desired.
# Using ARGs avoids GitHub API rate-limit failures during CI builds.
ARG XRAY_VERSION=v26.10.21
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
      amd64|x86_64)  XRAY_ASSET=Xray-linux-64.zip; XRAY_SHA256=0eec3ed4163c273d8c8bf821f7d219e257921cf584aa3fb970715e458e631978 ;; \
      arm64|aarch64) XRAY_ASSET=Xray-linux-arm64-v8a.zip; XRAY_SHA256=d1304946c486d98e6b886e9b931eaaaebb23835b30e217638037b7f2721d4e9c ;; \
      *) echo "unsupported arch: $ARCH" >&2; exit 1 ;; \
    esac && \
    wget -O /tmp/xray.zip "https://github.com/XTLS/Xray-core/releases/download/${XRAY_VERSION}/${XRAY_ASSET}" && \
    echo "${XRAY_SHA256}  /tmp/xray.zip" | sha256sum -c - && \
    unzip /tmp/xray.zip xray geoip.dat geosite.dat -d /usr/local/bin/ && \
    chmod +x /usr/local/bin/xray && \
    rm -f /tmp/xray.zip && \
    xray version

# Download Sing-box binary (required for Hysteria2, TUIC, and alternative engine)
RUN set -ex && \
    ARCH="${TARGETARCH:-$(apk --print-arch)}" && \
    case "$ARCH" in \
      amd64|x86_64)  SB_ARCH=amd64; SINGBOX_SHA256=8f6cb4bcf94d2b33c65d52e0d5b142db29a938336f1ff7267f397ac3758fc297 ;; \
      arm64|aarch64) SB_ARCH=arm64; SINGBOX_SHA256=675297394f9430cebb72b3c48ba8bce0d6f7c750a9d68a8f7f88c515c8255cd1 ;; \
      *) echo "unsupported arch: $ARCH" >&2; exit 1 ;; \
    esac && \
    SINGBOX_VER_STRIP="${SINGBOX_VERSION#v}" && \
    SB_BASE="https://github.com/SagerNet/sing-box/releases/download/${SINGBOX_VERSION}" && \
    # Alpine requires the musl build. Pin each architecture's release checksum.
    SB_NAME="sing-box-${SINGBOX_VER_STRIP}-linux-${SB_ARCH}-musl" && \
    wget -O /tmp/singbox.tar.gz "${SB_BASE}/${SB_NAME}.tar.gz" && \
    echo "${SINGBOX_SHA256}  /tmp/singbox.tar.gz" | sha256sum -c - && \
    tar -xzf /tmp/singbox.tar.gz -C /tmp && \
    cp /tmp/${SB_NAME}/sing-box /usr/local/bin/sing-box && \
    chmod 755 /usr/local/bin/sing-box && \
    rm -rf /tmp/singbox.tar.gz /tmp/sing-box-* && \
    sing-box version

# Copy backend binary (frontend is already embedded via go:embed)
COPY --from=backend-builder /zenithpanel /opt/zenithpanel/zenithpanel
COPY --from=cosign-builder /out/cosign /usr/local/bin/cosign

# Package vps_check.sh so diagnostics work in container deployments
COPY scripts/vps_check.sh /opt/zenithpanel/scripts/vps_check.sh
COPY scripts/proxy-runner.sh /opt/zenithpanel/scripts/proxy-runner.sh
RUN chmod +x /opt/zenithpanel/scripts/vps_check.sh /opt/zenithpanel/scripts/proxy-runner.sh

# Ensure the database and logs directories exist
RUN mkdir -p /opt/zenithpanel/data /opt/zenithpanel/logs

# Environment variables
ENV GIN_MODE=release
ENV TZ=Asia/Shanghai

CMD ["/opt/zenithpanel/zenithpanel"]
