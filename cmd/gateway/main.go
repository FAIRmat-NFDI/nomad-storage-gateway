package main

import (
	"log"
	"log/slog"
	"os"

	"github.com/FAIRmat-NFDI/nomad-storage-gateway/internal/config"
	"github.com/FAIRmat-NFDI/nomad-storage-gateway/internal/seaweedfs"
	"github.com/FAIRmat-NFDI/nomad-storage-gateway/internal/server"
)

func main() {
	logger := slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{
		Level: slog.LevelDebug,
	}))

	cfg, err := config.Load("config.yaml")
	if err != nil {
		log.Fatal(err)
	}
	filerClient, err := seaweedfs.NewFilerClient(cfg, logger)
	if err != nil {
		log.Fatal(err)
	}
	if err := server.Run(cfg, filerClient); err != nil {
		_ = filerClient.Close()
		log.Fatal(err)
	}

	if err := filerClient.Close(); err != nil {
		log.Fatal(err)
	}
}
