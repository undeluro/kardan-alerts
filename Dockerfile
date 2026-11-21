# syntax=docker/dockerfile:1

# ---- web UI -------------------------------------------------------------------
FROM node:24-alpine AS web
WORKDIR /src/web
COPY web/package.json web/package-lock.json ./
RUN npm ci --no-fund --no-audit
COPY web/ ./
RUN npm run build

# ---- Go binary (UI embedded) ---------------------------------------------------
FROM golang:1.26-alpine AS build
WORKDIR /src
ENV CGO_ENABLED=0 GOTOOLCHAIN=auto
COPY go.mod go.sum ./
RUN --mount=type=cache,target=/go/pkg/mod go mod download
COPY . .
COPY --from=web /src/web/dist ./web/dist
ARG VERSION=dev
RUN --mount=type=cache,target=/go/pkg/mod --mount=type=cache,target=/root/.cache/go-build \
    go build -trimpath -ldflags="-s -w -X github.com/undeluro/kardan-alerts/internal/app.Version=${VERSION}" \
      -o /out/kardan ./cmd/kardan && \
    go build -trimpath -ldflags="-s -w" -o /out/fakegw ./cmd/fakegw && \
    go build -trimpath -ldflags="-s -w" -o /out/mockops ./cmd/mockops && \
    go build -trimpath -ldflags="-s -w" -o /out/loadgen ./cmd/loadgen

# ---- runtime: no shell, no package manager, non-root ---------------------------
FROM gcr.io/distroless/static-debian12:nonroot
WORKDIR /app
COPY --from=build /out/kardan /out/fakegw /out/mockops /out/loadgen /usr/local/bin/
COPY config /app/config
ENV CONFIG_DIR=/app/config HTTP_ADDR=:8080
EXPOSE 8080
HEALTHCHECK --interval=5s --timeout=3s --start-period=20s --retries=5 CMD ["kardan", "healthcheck"]
ENTRYPOINT ["kardan"]
