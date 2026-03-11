# ZenithPanel Development Guide

[简体中文](development_guide_CN.md) | English

Welcome to ZenithPanel development! This guide will help you set up your local environment, understand the project structure, and follow our best practices.

---

## 1. 🛠️ Environment Prerequisites

ZenithPanel is a decoupled project. During development, you will run the backend API service and the frontend Dev Server separately.

### Dependencies
1. **Go (>= 1.24)**: Backend development.
2. **Node.js (>= 20)** & **npm (>= 10)**: Frontend Vue 3 development.
3. **SQLite3**: Handled automatically by the Go driver.
4. (Optional) **Docker**: Required for testing container management features.

### Setup Steps
```bash
# Clone the repository
git clone https://github.com/harveyxiacn/ZenithPanel.git
cd ZenithPanel

# 1. Start the Backend
cd backend
go mod tidy
go run main.go

# 2. Start the Frontend
cd frontend
npm install
npm run dev
```

> **Note**: The frontend Dev Server uses Vite's proxy configuration to forward `/api` requests to the backend on port 8080.

---

## 2. 📂 Project Structure

```text
ZenithPanel/
├── backend/                  # Go backend source
│   ├── internal/             # Core logic (private)
│   │   ├── api/              # Controllers & Asset embedding
│   │   ├── model/            # Database entities (GORM)
│   │   └── service/          # Business logic (Proxy, FS, Monitoring)
│   └── main.go               # Application entry point
├── frontend/                 # Vue 3 frontend source
│   ├── src/
│   │   ├── api/              # Axios clients
│   │   ├── components/       # Shared UI components
│   │   └── views/            # Main pages
│   └── vite.config.ts
├── docs/                     # Documentation
├── scripts/                  # Build and install scripts
├── .gitignore
└── README.md
```

---

## 3. 🛡️ Coding Standards

- **Backend**: Follow official Go idioms. Use the `internal/` directory to prevent external package importing of core logic.
- **Frontend**: Use strong TypeScript typing for all state and API responses. Styles should primarily use Tailwind CSS classes.

---

## 4. 📦 Build & Distribution

This project supports **Single Binary** distribution:
1. `npm run build` in the frontend directory generates static assets.
2. Assets are synced to `backend/internal/api/dist/`.
3. `go build` uses the `go:embed` directive to bundle assets into the final binary.
