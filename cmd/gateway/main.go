package main

import (
	"log"

	config "github.com/FAIRmat-NFDI/nomad-storage-gateway/internal/config"
	server "github.com/FAIRmat-NFDI/nomad-storage-gateway/internal/server"
)

func main() {
	cfg, err := config.Load("config.yaml")
	if err != nil {
		log.Fatal(err)
	}
	if err := server.Run(cfg); err != nil {
		log.Fatal(err)
	}
}
