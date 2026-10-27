# syntax=docker/dockerfile:1
# Multi-arch build: `docker buildx build --platform linux/amd64,linux/arm64 .`
# The frontend and Go stages run on the build host's native platform
# ($BUILDPLATFORM) and cross-compile for $TARGETARCH, so arm64 images don't
# pay the QEMU emulation cost for npm/go.

# Build stage for Frontend (architecture-independent output)
FROM --platform=$BUILDPLATFORM node:24-alpine@sha256:a0b9bf06e4e6193cf7a0f58816cc935ff8c2a908f81e6f1a95432d679c54fbfd AS frontend-builder
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

# Build upstream Sing-box with the security-patched dependency lock in
# tools/singbox. The same build script is used by IPv6 CI so production and
# test protocol behavior share the official feature tags and linker settings.
FROM --platform=$BUILDPLATFORM golang:1.26.10-alpine@sha256:a2012b4a1d1f9f7e9f44e0d39b805a901e856b1dd80b4e6ee4338db547050308 AS singbox-builder
ARG TARGETOS
ARG TARGETARCH
WORKDIR /src
COPY tools/singbox/go.mod tools/singbox/go.sum ./
COPY tools/singbox/build.sh ./build.sh
RUN chmod 755 ./build.sh && go mod download all
RUN GOOS=${TARGETOS:-linux} GOARCH=${TARGETARCH:-amd64} SINGBOX_OUTPUT=/out/sing-box ./build.sh

# Final Runtime Image
FROM alpine:3.24@sha256:be2656061b536c1883c4034790e21d0a09c5aaaf51968b89368a3c0371ea2c9a
WORKDIR /opt/zenithpanel

# Pinned proxy engine versions — update these when a new release is desired.
# Using ARGs avoids GitHub API rate-limit failures during CI builds.
ARG XRAY_VERSION=v26.10.21
ARG COSIGN_VERSION=v3.1.4
# Set automatically by BuildKit to the platform being built (amd64 / arm64).
ARG TARGETARCH

# Install basic runtime dependencies (ca-certificates for TLS, tzdata, etc).
# iproute2 provides `ss`, used by the traffic-egress socket sampler to attribute
# host-wide outbound flows to proxy processes.
# The digest-pinned base can be cached with an older OpenSSL patch level; update
# these runtime packages from Alpine's signed 3.23 repository before installing
# panel dependencies.
RUN apk upgrade --no-cache libcrypto3 libssl3 && \
    apk add --no-cache ca-certificates tzdata sqlite-libs docker-cli bash iptables iproute2 util-linux unzip

# Download the official, patched OTA signature verifier rather than building a
# stale transitive dependency tree. Verify both supported architecture assets.
RUN set -ex && \
    ARCH="${TARGETARCH:-$(apk --print-arch)}" && \
    case "$ARCH" in \
      amd64|x86_64)  COSIGN_ARCH=amd64; COSIGN_SHA256=424e2c97d0b45fd7a6b608a87f090f35633f4c23ece5f092d48bba11aaf4bbfa ;; \
      arm64|aarch64) COSIGN_ARCH=arm64; COSIGN_SHA256=ade83b7aac62e131cacfe96d958948b2150cc9efec46a570a2649b8e27dedad2 ;; \
      *) echo "unsupported arch: $ARCH" >&2; exit 1 ;; \
    esac && \
    wget -O /tmp/cosign "https://github.com/sigstore/cosign/releases/download/${COSIGN_VERSION}/cosign-linux-${COSIGN_ARCH}" && \
    echo "${COSIGN_SHA256}  /tmp/cosign" | sha256sum -c - && \
    install -m 755 /tmp/cosign /usr/local/bin/cosign && \
    rm -f /tmp/cosign && \
    cosign version

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

# Copy backend binary (frontend is already embedded via go:embed)
COPY --from=backend-builder /zenithpanel /opt/zenithpanel/zenithpanel
COPY --from=singbox-builder /out/sing-box /usr/local/bin/sing-box

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
