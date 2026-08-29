package seaweedfs

import (
	"context"
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
		Time:                1 * time.Minute, // Send ping every 1m
		Timeout:             5 * time.Second, // Wait 5s for ping ack
		PermitWithoutStream: false,           // Do not ping when connection is idle
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

func (f *FilerClient) LookupDirectoryEntry(
	ctx context.Context,
	request *filer_pb.LookupDirectoryEntryRequest,
	opts ...grpc.CallOption,
) (*filer_pb.LookupDirectoryEntryResponse, error) {
	return f.Client.LookupDirectoryEntry(ctx, request, opts...)
}
