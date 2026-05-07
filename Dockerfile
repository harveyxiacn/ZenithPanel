# Build stage for Frontend
FROM node:20-alpine AS frontend-builder
WORKDIR /app/frontend
COPY frontend/package*.json ./
RUN npm install
COPY frontend .
RUN npm run build

# Build stage for Backend (Go)
FROM golang:1.25-alpine AS backend-builder
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
ENV GOOS=linux
ENV GOARCH=amd64
RUN go build -o /zenithpanel main.go

# Final Runtime Image
FROM alpine:latest
WORKDIR /opt/zenithpanel

# Pinned proxy engine versions — update these when a new release is desired.
# Using ARGs avoids GitHub API rate-limit failures during CI builds.
ARG XRAY_VERSION=v25.4.30
ARG SINGBOX_VERSION=v1.11.0

# Install basic runtime dependencies (ca-certificates for TLS, tzdata, etc)
RUN apk add --no-cache ca-certificates tzdata sqlite-libs docker-cli bash iptables util-linux unzip

# Download Xray-core binary + geodata
RUN set -ex && \
    wget -O /tmp/xray.zip "https://github.com/XTLS/Xray-core/releases/download/${XRAY_VERSION}/Xray-linux-64.zip" && \
    unzip /tmp/xray.zip xray geoip.dat geosite.dat -d /usr/local/bin/ && \
    chmod +x /usr/local/bin/xray && \
    rm -f /tmp/xray.zip && \
    xray version

# Download Sing-box binary (required for Hysteria2, TUIC, and alternative engine)
RUN set -ex && \
    SINGBOX_VER_STRIP="${SINGBOX_VERSION#v}" && \
    wget -O /tmp/singbox.tar.gz "https://github.com/SagerNet/sing-box/releases/download/${SINGBOX_VERSION}/sing-box-${SINGBOX_VER_STRIP}-linux-amd64.tar.gz" && \
    tar -xzf /tmp/singbox.tar.gz -C /tmp && \
    cp /tmp/sing-box-${SINGBOX_VER_STRIP}-linux-amd64/sing-box /usr/local/bin/sing-box && \
    chmod 755 /usr/local/bin/sing-box && \
    rm -rf /tmp/singbox.tar.gz /tmp/sing-box-* && \
    sing-box version

# Copy backend binary (frontend is already embedded via go:embed)
COPY --from=backend-builder /zenithpanel /opt/zenithpanel/zenithpanel

# Ensure the database and logs directories exist
RUN mkdir -p /opt/zenithpanel/data /opt/zenithpanel/logs

# Environment variables
ENV GIN_MODE=release
ENV TZ=Asia/Shanghai

CMD ["/opt/zenithpanel/zenithpanel"]
