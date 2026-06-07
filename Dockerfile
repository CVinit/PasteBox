# syntax=docker/dockerfile:1.7

ARG GO_VERSION=1.26
ARG NODE_VERSION=26

FROM node:${NODE_VERSION}-bookworm-slim AS web-builder
WORKDIR /src/web
COPY web/package.json web/package-lock.json ./
RUN npm ci
COPY web/ ./
RUN npm run build

FROM golang:${GO_VERSION}-bookworm AS api-builder
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . ./
RUN rm -rf ./internal/httpserver/static
COPY --from=web-builder /src/web/dist/ ./internal/httpserver/static/
RUN CGO_ENABLED=0 GOOS=linux go build -trimpath -ldflags="-s -w" -o /out/pastebox ./cmd/pastebox

FROM alpine:3.22
RUN apk add --no-cache ca-certificates tzdata && \
    addgroup -S pastebox && \
    adduser -S -G pastebox -H -h /nonexistent pastebox
COPY --from=api-builder /out/pastebox /usr/local/bin/pastebox

ENV PASTEBOX_APP_ENV=production \
    PASTEBOX_APP_NAME=PasteBox \
    PASTEBOX_HTTP_ADDR=:8080

USER pastebox
EXPOSE 8080
HEALTHCHECK --interval=30s --timeout=5s --start-period=10s --retries=3 \
  CMD wget -qO- http://127.0.0.1:8080/readyz >/dev/null || exit 1
ENTRYPOINT ["/usr/local/bin/pastebox"]
