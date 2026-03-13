# Build stage for Frontend
FROM node:20-alpine AS frontend-builder
WORKDIR /app/frontend
COPY frontend/package*.json ./
RUN npm install
COPY frontend .
RUN npm run build

# Build stage for Backend (Go)
FROM golang:1.24-alpine AS backend-builder
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

# Install basic runtime dependencies (ca-certificates for TLS, tzdata, etc)
RUN apk add --no-cache ca-certificates tzdata sqlite-libs docker-cli bash iptables util-linux unzip

# Download latest Xray-core binary + geodata
RUN set -ex && \
    XRAY_VER=$(wget -qO- "https://api.github.com/repos/XTLS/Xray-core/releases/latest" | grep '"tag_name"' | head -1 | cut -d'"' -f4) && \
    wget -O /tmp/xray.zip "https://github.com/XTLS/Xray-core/releases/download/${XRAY_VER}/Xray-linux-64.zip" && \
    unzip /tmp/xray.zip xray geoip.dat geosite.dat -d /usr/local/bin/ && \
    chmod +x /usr/local/bin/xray && \
    rm -f /tmp/xray.zip && \
    xray version

# Copy backend binary (frontend is already embedded via go:embed)
COPY --from=backend-builder /zenithpanel /opt/zenithpanel/zenithpanel

# Ensure the database and logs directories exist
RUN mkdir -p /opt/zenithpanel/data /opt/zenithpanel/logs

EXPOSE 8080

# Environment variables
ENV GIN_MODE=release
ENV TZ=Asia/Shanghai

CMD ["/opt/zenithpanel/zenithpanel"]
