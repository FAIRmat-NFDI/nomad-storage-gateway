package seaweedfs

import (
	"log/slog"
	"time"

	"github.com/FAIRmat-NFDI/nomad-storage-gateway/internal/config"
	"github.com/seaweedfs/seaweedfs/weed/pb/filer_pb"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/keepalive"
)

type FilerClient struct {
	conn   *grpc.ClientConn
	Client filer_pb.SeaweedFilerClient
	logger *slog.Logger
}

func NewFilerClient(cfg config.Config, logger *slog.Logger) (*FilerClient, error) {
	kacp := keepalive.ClientParameters{
		Time:                10 * time.Second, // Send ping every 10s if idle
		Timeout:             2 * time.Second,  // Wait 2s for ping ack
		PermitWithoutStream: true,             // Keep-alive even when no active RPCs
	}
	conn, err := grpc.NewClient(
		cfg.SeaweedFS.FilerEndpoint,
		grpc.WithTransportCredentials(insecure.NewCredentials()),
		grpc.WithKeepaliveParams(kacp),
	)
	if err != nil {
		return nil, err
	}
	return &FilerClient{
		conn:   conn,
		Client: filer_pb.NewSeaweedFilerClient(conn),
		logger: logger,
	}, nil
}

func (f *FilerClient) Close() error {
	return f.conn.Close()
}
