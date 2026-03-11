# Build stage for Frontend
FROM node:20-alpine AS frontend-builder
WORKDIR /app/frontend
COPY frontend/package*.json ./
RUN npm install
COPY frontend .
RUN npm run build

# Build stage for Backend (Go)
FROM golang:1.21-alpine AS backend-builder
WORKDIR /app/backend
# Install dependencies needed for cgo if ever required, though we use pure go sqlite now
RUN apk add --no-cache gcc musl-dev
COPY backend/go.mod backend/go.sum ./
RUN go mod download
COPY backend .
# Disable CGO to ensure statically linked binary
ENV CGO_ENABLED=0
ENV GOOS=linux
ENV GOARCH=amd64
RUN go build -o /zenithpanel main.go

# Final Runtime Image
FROM alpine:latest
WORKDIR /opt/zenithpanel

# Install basic runtime dependencies (ca-certificates for TLS, tzdata, etc)
RUN apk add --no-cache ca-certificates tzdata sqlite-libs docker-cli tzdata

# Copy backend binary
COPY --from=backend-builder /zenithpanel /opt/zenithpanel/zenithpanel

# Copy frontend static build (the backend router serves these from ../frontend/dist by default, 
# but in docker we can put them in a specific absolute path or embed them. 
# Assuming the Go binary is modified to serve from ./frontend/dist in docker)
COPY --from=frontend-builder /app/frontend/dist /opt/zenithpanel/frontend/dist

# Ensure the database and logs directories exist
RUN mkdir -p /opt/zenithpanel/data /opt/zenithpanel/logs

EXPOSE 8080

# Environment variables
ENV GIN_MODE=release
ENV TZ=Asia/Shanghai

CMD ["/opt/zenithpanel/zenithpanel"]
