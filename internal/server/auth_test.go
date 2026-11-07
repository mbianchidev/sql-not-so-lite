package server

import (
	"context"
	"errors"
	"io"
	"net"
	"testing"
	"time"

	pb "github.com/mbianchidev/sql-not-so-lite/api/proto"
	"github.com/mbianchidev/sql-not-so-lite/internal/config"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/metadata"
	reflectionpb "google.golang.org/grpc/reflection/grpc_reflection_v1"
	"google.golang.org/grpc/status"
	"google.golang.org/grpc/test/bufconn"
)

func TestGRPCAuthenticationIncludesReflectionAndCatalogLifecycle(t *testing.T) {
	httpServer, cat := setupHTTPTestServer(t)
	cfg := config.DefaultConfig()
	cfg.Server.AuthToken = fixtureAdminToken
	server := NewGRPCServer(httpServer.svc, 0, cat, cfg).handler()
	listener := bufconn.Listen(1024 * 1024)
	done := make(chan error, 1)
	go func() { done <- server.Serve(listener) }()
	t.Cleanup(func() {
		server.Stop()
		listener.Close()
		if err := <-done; err != nil && err != grpc.ErrServerStopped {
			t.Errorf("serve: %v", err)
		}
	})
	conn, err := grpc.NewClient("passthrough:///fixture", grpc.WithTransportCredentials(insecure.NewCredentials()), grpc.WithContextDialer(func(context.Context, string) (net.Conn, error) { return listener.Dial() }))
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	client := pb.NewSqlNotSoLiteClient(conn)
	if _, err := client.Ping(ctx, &pb.PingRequest{}); status.Code(err) != codes.Unauthenticated {
		t.Fatalf("unauthorized ping: %v", err)
	}
	authorized := metadata.AppendToOutgoingContext(ctx, "authorization", "Bearer "+fixtureAdminToken)
	if _, err := client.Ping(authorized, &pb.PingRequest{}); err != nil {
		t.Fatal(err)
	}
	stream, err := reflectionpb.NewServerReflectionClient(conn).ServerReflectionInfo(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if err := stream.Send(&reflectionpb.ServerReflectionRequest{MessageRequest: &reflectionpb.ServerReflectionRequest_ListServices{ListServices: ""}}); err != nil && !errors.Is(err, io.EOF) {
		t.Fatal(err)
	}
	if _, err := stream.Recv(); status.Code(err) != codes.Unauthenticated {
		t.Fatalf("reflection bypassed auth: %v", err)
	}
	created, err := client.CreateDatabase(authorized, &pb.CreateDatabaseRequest{Name: "rpc_fixture"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := cat.GetDiscoveredByPath(created.Path); err != nil {
		t.Fatalf("RPC database not catalogued: %v", err)
	}
	if _, err := client.DropDatabase(authorized, &pb.DropDatabaseRequest{Name: "rpc_fixture"}); err != nil {
		t.Fatal(err)
	}
}
