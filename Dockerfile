# syntax=docker/dockerfile:1

# --- frontend ---
FROM node:22-alpine AS frontend
WORKDIR /web
COPY web/package.json web/pnpm-lock.yaml web/pnpm-workspace.yaml web/.npmrc ./
RUN corepack enable && corepack prepare pnpm@latest --activate && pnpm install --frozen-lockfile
COPY web/ ./
RUN pnpm build

# --- go build ---
FROM golang:1.26-alpine AS build
RUN apk add --no-cache ca-certificates
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
COPY --from=frontend /cmd/server/web/dist ./cmd/server/web/dist
RUN mkdir /data
RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /out/server ./cmd/server

# --- runtime ---
FROM scratch
COPY --from=build /etc/ssl/certs/ca-certificates.crt /etc/ssl/certs/ca-certificates.crt
COPY --from=build --chown=65532:65532 /data /data
COPY --from=build /out/server /app/server

ENV LISTEN_ADDR=:8080
ENV DB_PATH=/data/game-status.db
EXPOSE 8080

WORKDIR /data
USER 65532:65532
ENTRYPOINT ["/app/server"]
