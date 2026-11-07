package server

import (
	"context"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"net/http"
	"strings"

	"github.com/mbianchidev/sql-not-so-lite/internal/config"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
)

func tokenHash(token string) string {
	sum := sha256.Sum256([]byte(token))
	return hex.EncodeToString(sum[:])
}

func validBearer(header, hash string) bool {
	parts := strings.Fields(header)
	if len(parts) != 2 || !strings.EqualFold(parts[0], "Bearer") || hash == "" {
		return false
	}
	actual := tokenHash(parts[1])
	return subtle.ConstantTimeCompare([]byte(actual), []byte(hash)) == 1
}

func serverSettings(cfg *config.Config) config.ServerConfig {
	if cfg == nil {
		return config.DefaultConfig().Server
	}
	return cfg.Server
}

func (s *HTTPServer) adminMiddleware(next http.HandlerFunc) http.HandlerFunc {
	hash := ""
	if token := serverSettings(s.cfg).AuthToken; token != "" {
		hash = tokenHash(token)
	}
	return func(w http.ResponseWriter, r *http.Request) {
		if hash != "" && !validBearer(r.Header.Get("Authorization"), hash) {
			w.Header().Set("WWW-Authenticate", `Bearer realm="sql-not-so-lite"`)
			writeError(w, http.StatusUnauthorized, "admin bearer token required")
			return
		}
		next(w, r)
	}
}

func grpcAuthorized(ctx context.Context, token string) bool {
	if token == "" {
		return true
	}
	md, ok := metadata.FromIncomingContext(ctx)
	if !ok {
		return false
	}
	headers := md.Get("authorization")
	return len(headers) == 1 && validBearer(headers[0], tokenHash(token))
}

func adminUnary(token string) grpc.UnaryServerInterceptor {
	return func(ctx context.Context, req any, info *grpc.UnaryServerInfo, handler grpc.UnaryHandler) (any, error) {
		if !grpcAuthorized(ctx, token) {
			return nil, status.Error(codes.Unauthenticated, "admin bearer token required")
		}
		return handler(ctx, req)
	}
}

func adminStream(token string) grpc.StreamServerInterceptor {
	return func(srv any, stream grpc.ServerStream, info *grpc.StreamServerInfo, handler grpc.StreamHandler) error {
		if !grpcAuthorized(stream.Context(), token) {
			return status.Error(codes.Unauthenticated, "admin bearer token required")
		}
		return handler(srv, stream)
	}
}
