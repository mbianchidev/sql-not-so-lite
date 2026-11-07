package server

import (
	"context"
	"fmt"
	"log"
	"net"
	"runtime"
	"time"

	pb "github.com/mbianchidev/sql-not-so-lite/api/proto"
	"github.com/mbianchidev/sql-not-so-lite/internal/catalog"
	"github.com/mbianchidev/sql-not-so-lite/internal/config"
	"github.com/mbianchidev/sql-not-so-lite/internal/service"
	"google.golang.org/grpc"
	"google.golang.org/grpc/reflection"
)

var Version = "dev"

type GRPCServer struct {
	pb.UnimplementedSqlNotSoLiteServer
	svc       *service.DatabaseService
	server    *grpc.Server
	startTime time.Time
	port      int
	catalog   *catalog.Catalog
	cfg       *config.Config
}

func NewGRPCServer(svc *service.DatabaseService, port int, cat *catalog.Catalog, cfg *config.Config) *GRPCServer {
	return &GRPCServer{
		svc:       svc,
		port:      port,
		startTime: time.Now(),
		catalog:   cat, cfg: cfg,
	}
}

func (s *GRPCServer) Start() error {
	settings := serverSettings(s.cfg)
	cfg := config.DefaultConfig()
	cfg.Server = settings
	if err := cfg.ValidateNetwork(); err != nil {
		return err
	}
	lis, err := net.Listen("tcp", net.JoinHostPort(settings.BindAddress, fmt.Sprint(s.port)))
	if err != nil {
		return fmt.Errorf("failed to listen on port %d: %w", s.port, err)
	}

	s.server = s.handler()
	log.Printf("gRPC server listening on %s", lis.Addr())
	return s.server.Serve(lis)
}

func (s *GRPCServer) handler() *grpc.Server {
	settings := serverSettings(s.cfg)
	result := grpc.NewServer(grpc.UnaryInterceptor(adminUnary(settings.AuthToken)), grpc.StreamInterceptor(adminStream(settings.AuthToken)))
	pb.RegisterSqlNotSoLiteServer(result, s)
	reflection.Register(result)
	return result
}

func (s *GRPCServer) Stop() {
	if s.server != nil {
		s.server.GracefulStop()
	}
}

func (s *GRPCServer) CreateDatabase(ctx context.Context, req *pb.CreateDatabaseRequest) (*pb.CreateDatabaseResponse, error) {
	info, err := s.svc.CreateDatabase(ctx, req.Name)
	if err != nil {
		return nil, err
	}
	if err := registerDatabase(s.catalog, info); err != nil {
		if rollback := s.svc.DropDatabase(ctx, info.Name); rollback != nil {
			return nil, fmt.Errorf("register database: %v; rollback: %w", err, rollback)
		}
		return nil, fmt.Errorf("register database: %w", err)
	}
	return &pb.CreateDatabaseResponse{
		Name: info.Name,
		Path: info.Path,
	}, nil
}

func (s *GRPCServer) ListDatabases(ctx context.Context, _ *pb.ListDatabasesRequest) (*pb.ListDatabasesResponse, error) {
	dbs, err := s.svc.ListDatabases(ctx)
	if err != nil {
		return nil, err
	}

	var pbDbs []*pb.DatabaseInfo
	for _, db := range dbs {
		pbDbs = append(pbDbs, &pb.DatabaseInfo{
			Name:       db.Name,
			Path:       db.Path,
			SizeBytes:  db.SizeBytes,
			Active:     db.Active,
			TableCount: db.TableCount,
		})
	}

	return &pb.ListDatabasesResponse{Databases: pbDbs}, nil
}

func (s *GRPCServer) DropDatabase(ctx context.Context, req *pb.DropDatabaseRequest) (*pb.DropDatabaseResponse, error) {
	info, err := s.svc.GetDatabaseInfo(ctx, req.Name)
	if err != nil {
		return nil, err
	}
	err = s.svc.DropDatabase(ctx, req.Name)
	if err != nil {
		return nil, err
	}
	if err := s.catalog.DeleteDiscoveredByPath(info.Path); err != nil {
		return nil, fmt.Errorf("database dropped but catalog cleanup failed: %w", err)
	}
	return &pb.DropDatabaseResponse{Success: true}, nil
}

func (s *GRPCServer) GetDatabaseInfo(ctx context.Context, req *pb.GetDatabaseInfoRequest) (*pb.GetDatabaseInfoResponse, error) {
	info, err := s.svc.GetDatabaseInfo(ctx, req.Name)
	if err != nil {
		return nil, err
	}
	return &pb.GetDatabaseInfoResponse{
		Database: &pb.DatabaseInfo{
			Name:       info.Name,
			Path:       info.Path,
			SizeBytes:  info.SizeBytes,
			Active:     info.Active,
			TableCount: info.TableCount,
		},
	}, nil
}

func (s *GRPCServer) Execute(ctx context.Context, req *pb.ExecuteRequest) (*pb.ExecuteResponse, error) {
	result, err := s.svc.Execute(ctx, req.Database, req.Sql, req.Params)
	if err != nil {
		return nil, err
	}
	return &pb.ExecuteResponse{
		RowsAffected: result.RowsAffected,
		LastInsertId: result.LastInsertID,
	}, nil
}

func (s *GRPCServer) Query(ctx context.Context, req *pb.QueryRequest) (*pb.QueryResponse, error) {
	result, err := s.svc.Query(ctx, req.Database, req.Sql, req.Params, int(req.Limit), int(req.Offset))
	if err != nil {
		return nil, err
	}

	var columns []*pb.Column
	for _, c := range result.Columns {
		columns = append(columns, &pb.Column{Name: c.Name, Type: c.Type})
	}

	var rows []*pb.Row
	for _, r := range result.Rows {
		rows = append(rows, &pb.Row{Values: r})
	}

	return &pb.QueryResponse{
		Columns:    columns,
		Rows:       rows,
		TotalCount: result.TotalCount,
	}, nil
}

func (s *GRPCServer) GetSchema(ctx context.Context, req *pb.GetSchemaRequest) (*pb.GetSchemaResponse, error) {
	tables, err := s.svc.GetSchema(ctx, req.Database)
	if err != nil {
		return nil, err
	}

	var pbTables []*pb.TableInfo
	for _, t := range tables {
		var cols []*pb.ColumnInfo
		for _, c := range t.Columns {
			cols = append(cols, &pb.ColumnInfo{
				Name:         c.Name,
				Type:         c.Type,
				Nullable:     c.Nullable,
				DefaultValue: c.DefaultValue,
				PrimaryKey:   c.PrimaryKey,
			})
		}

		var idxs []*pb.IndexInfo
		for _, idx := range t.Indexes {
			idxs = append(idxs, &pb.IndexInfo{
				Name:    idx.Name,
				Columns: idx.Columns,
				Unique:  idx.Unique,
			})
		}

		pbTables = append(pbTables, &pb.TableInfo{
			Name:     t.Name,
			Columns:  cols,
			Indexes:  idxs,
			RowCount: t.RowCount,
		})
	}

	return &pb.GetSchemaResponse{Tables: pbTables}, nil
}

func (s *GRPCServer) Ping(_ context.Context, _ *pb.PingRequest) (*pb.PingResponse, error) {
	var memStats runtime.MemStats
	runtime.ReadMemStats(&memStats)

	return &pb.PingResponse{
		Version:         Version,
		UptimeSeconds:   int64(time.Since(s.startTime).Seconds()),
		ActiveDatabases: int32(s.svc.ActiveCount()),
		MemoryBytes:     int64(memStats.Alloc),
	}, nil
}
