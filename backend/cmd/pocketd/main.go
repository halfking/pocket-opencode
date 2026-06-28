package main

import (
	"log"
	"net/http"
	"os"
	"path/filepath"
	"strconv"

	"github.com/halfking/pocket-opencode/backend/internal/adapter"
	"github.com/halfking/pocket-opencode/backend/internal/config"
	"github.com/halfking/pocket-opencode/backend/internal/registry"
	"github.com/halfking/pocket-opencode/backend/internal/server"
	"github.com/halfking/pocket-opencode/backend/internal/task"
)

func main() {
	cfg := config.Load()

	// Ensure data directory exists
	dataDir := filepath.Dir(cfg.DBPath)
	if err := os.MkdirAll(dataDir, 0755); err != nil {
		log.Fatalf("Failed to create data directory: %v", err)
	}

	// Initialize task store
	taskStore, err := task.NewStore(cfg.DBPath)
	if err != nil {
		log.Fatalf("Failed to initialize task store: %v", err)
	}
	defer taskStore.Close()
	log.Printf("Task store initialized: %s", cfg.DBPath)

	// Initialize NPS adapter
	var npsAdapter adapter.NPSAdapter
	if cfg.NPSAuthKey != "" {
		log.Printf("Using NPS Web API adapter: %s", cfg.NPSBaseURL)
		npsAdapter = adapter.NewNPSWebAPIAdapter(cfg.NPSBaseURL, cfg.NPSAuthKey)
	} else {
		log.Println("Using static NPS adapter (demo mode)")
		npsAdapter = adapter.NewStaticNPSAdapter()
	}

	// Initialize OpenCode adapter
	timeoutMS, _ := strconv.Atoi(cfg.OpenCodeTimeoutMS)
	if timeoutMS == 0 {
		timeoutMS = 5000
	}
	opencodeAdapter := adapter.NewOpenCodeHTTPAdapter(timeoutMS)

	// Initialize OpenCode Config adapter
	configAdapter := adapter.NewOpenCodeConfigHTTPAdapter(timeoutMS)

	// Initialize Registry
	reg := registry.NewRegistry()
	if cfg.OpenCodeInstancesJSON != "" {
		configs, err := registry.ParseConfigJSON(cfg.OpenCodeInstancesJSON)
		if err != nil {
			log.Printf("Warning: Failed to parse OpenCode instances config: %v", err)
		} else {
			if err := reg.LoadFromConfig(configs); err != nil {
				log.Printf("Warning: Failed to load instances from config: %v", err)
			} else {
				log.Printf("Loaded %d OpenCode instances from config", len(configs))
			}
		}
	}

	srv := server.New(cfg, npsAdapter, opencodeAdapter, taskStore, reg, configAdapter)

	addr := ":" + cfg.HTTPPort
	log.Printf("pocketd listening on %s", addr)
	if err := http.ListenAndServe(addr, srv.Handler()); err != nil {
		log.Fatal(err)
	}
}
