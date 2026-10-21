FROM node:22-alpine AS gui-builder
WORKDIR /app/web
COPY web/package*.json web/.npmrc ./
COPY web/scripts/ ./scripts/
RUN npm ci
COPY web/ ./
RUN npm run build

FROM golang:1.27.3-alpine AS go-builder
WORKDIR /app
RUN apk add --no-cache protobuf
COPY go.mod go.sum ./
RUN go mod download
COPY . .
COPY --from=gui-builder /app/internal/server/static ./internal/server/static
ARG VERSION=dev
RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w -X github.com/mbianchidev/sql-not-so-lite/internal/server.Version=${VERSION}" -o /sqnsl ./cmd/sqnsl/

FROM alpine:3.24
RUN apk add --no-cache ca-certificates
COPY --from=go-builder /sqnsl /usr/local/bin/sqnsl

RUN mkdir -p /data
ENV HOME=/data SQNSL_DATA_DIR=/data SQNSL_SCAN_ROOT=/data
WORKDIR /data

EXPOSE 50051 9147
VOLUME ["/data"]

ENTRYPOINT ["sqnsl"]
CMD ["start"]
