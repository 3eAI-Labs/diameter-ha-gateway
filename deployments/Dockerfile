# Multi-stage build for Diameter HA Gateway

# Stage 1: Build Frontend
FROM node:18-alpine AS frontend-builder
WORKDIR /app/web
COPY web/package.json ./
RUN npm install
COPY web/ .
RUN npm run build
# Licence texts of the npm packages bundled into web/dist, from the tree just installed; the
# build fails if one is outside the allowed licences.
COPY scripts/npm-licenses.mjs /app/scripts/
RUN node /app/scripts/npm-licenses.mjs /app/web /app/npm-licenses

# Stage 2: Build Backend
# Base must satisfy go.mod's `go 1.25.5`. Module downloads are a cached layer:
# go.mod and go.sum are copied before the sources.
FROM golang:1.25-alpine AS builder

WORKDIR /app

COPY go.mod go.sum ./
RUN go mod download

COPY . .

RUN CGO_ENABLED=0 GOOS=linux go build -trimpath -ldflags='-s -w' -o main ./cmd/diameter-gateway

# Licence texts (and NOTICE files) of every third-party module compiled into the binary, for
# redistribution of the image (Apache-2.0 §4(d), MIT/BSD notice clauses, MPL-2.0 source offer).
# The gateway's own packages are skipped (its LICENSE/NOTICE are copied below); the trailing
# slash keeps the Apache-2.0 client module, which is compiled in, in the saved set.
RUN go install github.com/google/go-licenses/v2@v2.0.1 && \
    go-licenses save ./cmd/diameter-gateway --save_path=/app/third_party_licenses \
        --ignore github.com/3eAI-Labs/diameter-ha-gateway/

# Final stage
FROM alpine:3.18

LABEL org.opencontainers.image.title="diameter-ha-gateway" \
      org.opencontainers.image.vendor="3eAI Labs Ltd" \
      org.opencontainers.image.licenses="AGPL-3.0-only" \
      org.opencontainers.image.source="https://github.com/3eAI-Labs/diameter-ha-gateway"

RUN apk --no-cache add ca-certificates tzdata

# WORKDIR /app (NOT /root): the app reads ./configs, and under runAsNonRoot the
# non-root uid must traverse it — /root is 0700 and would break config load,
# leaving nats.url empty and CrashLooping the pod.
WORKDIR /app

# Copy binary and configs
COPY --from=builder /app/main .
COPY --from=builder /app/configs ./configs
# Copy frontend assets
COPY --from=frontend-builder /app/web/dist ./web/dist

# Licences: the gateway's own, and those of the Go modules and npm packages built into it
COPY LICENSE NOTICE /usr/share/licenses/diameter-ha-gateway/
COPY --from=builder /app/third_party_licenses /usr/share/licenses/diameter-ha-gateway/third_party/go/
COPY --from=frontend-builder /app/npm-licenses /usr/share/licenses/diameter-ha-gateway/third_party/npm/

# [DEVOPS-2.1] Security hardening: Non-root user (owns /app so config is readable)
RUN addgroup -g 1000 -S appuser && \
    adduser -u 1000 -S appuser -G appuser && \
    chown -R 1000:1000 /app
USER appuser

# [DEVOPS-2.1] Health check (Metrics endpoint used as health check)
HEALTHCHECK --interval=30s --timeout=3s --start-period=5s --retries=3 \
  CMD wget --no-verbose --tries=1 --spider http://localhost:8080/metrics || exit 1

# Diameter Port (SCTP/TCP)
EXPOSE 3868
# Admin API Port
EXPOSE 8081
# Metrics Port
EXPOSE 8080

CMD ["./main"]
