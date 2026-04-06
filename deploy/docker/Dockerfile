FROM node:22-alpine AS gui-builder
WORKDIR /app/web
COPY web/package*.json ./
RUN npm ci
COPY web/ ./
RUN npm run build

FROM golang:1.23-alpine AS go-builder
WORKDIR /app
RUN apk add --no-cache protobuf
COPY go.mod go.sum ./
RUN go mod download
COPY . .
COPY --from=gui-builder /app/internal/server/static ./internal/server/static
RUN CGO_ENABLED=0 go build -ldflags="-s -w" -o /sqnsl ./cmd/sqnsl/

FROM alpine:3.20
RUN apk add --no-cache ca-certificates
COPY --from=go-builder /sqnsl /usr/local/bin/sqnsl

RUN mkdir -p /data
ENV SQNSL_DATA_DIR=/data

EXPOSE 50051 9147
VOLUME ["/data"]

ENTRYPOINT ["sqnsl"]
CMD ["start"]
