package main

import (
	"backend-core/internal/agent/client"
	"backend-core/internal/agent/config"
	"backend-core/internal/agent/handler"
	"backend-core/internal/agent/monitor"
	"backend-core/internal/agent/vm"
	"backend-core/pkg/contracts"
	"context"
	"flag"
	"log"
	"os"
	"time"
)

func main() {
	cfgPath := flag.String("config", "agent.yaml", "path to agent YAML config file")
	flag.Parse()

	// Load config from YAML file; fall back to defaults + env overrides if file not found
	cfg, err := config.LoadFromFile(*cfgPath)
	if err != nil {
		log.Printf("[agent] could not load config file %s: %v (using defaults + env)", *cfgPath, err)
		cfg = config.DefaultConfig()
	}

	// Environment variables override YAML values when set
	if v := os.Getenv("AGENT_NODE_ID"); v != "" {
		cfg.NodeID = v
	}
	if v := os.Getenv("AGENT_SECRET"); v != "" {
		cfg.Secret = v
	}
	if v := os.Getenv("AGENT_GRPC_ADDRESS"); v != "" {
		cfg.GRPCAddress = v
	}
	if v := os.Getenv("AGENT_VIRT_BACKEND"); v != "" {
		cfg.VirtBackend = v
	}
	if uri := os.Getenv("AGENT_LIBVIRT_URI"); uri != "" {
		cfg.VirtOpts["uri"] = uri
	}
	if proj := os.Getenv("AGENT_INCUS_PROJECT"); proj != "" {
		cfg.VirtOpts["project"] = proj
	}
	if sock := os.Getenv("AGENT_INCUS_SOCKET"); sock != "" {
		cfg.VirtOpts["socket"] = sock
	}

	driver, err := vm.NewHypervisor(vm.Backend(cfg.VirtBackend), cfg.VirtOpts)
	if err != nil {
		log.Fatalf("[agent] failed to create hypervisor: %v", err)
	}

	// Connect to the controller via gRPC
	grpcClient, err := client.Dial(cfg.GRPCAddress)
	if err != nil {
		log.Fatalf("[agent] failed to connect to controller: %v", err)
	}
	defer grpcClient.Close()

	log.Printf("[agent] starting node=%s grpc=%s backend=%s", cfg.NodeID, cfg.GRPCAddress, cfg.VirtBackend)

	// 1. Register with the controller
	reg := contracts.AgentRegistration{
		NodeID:   cfg.NodeID,
		Secret:   cfg.Secret,
		Hostname: cfg.NodeID,
		Location: cfg.Location,
		IP:       "127.0.0.1",
		Version:  "v0.1.0",
	}
	ctx := context.Background()
	if err := grpcClient.Register(ctx, reg); err != nil {
		log.Printf("[agent] registration failed (will retry on heartbeat): %v", err)
	} else {
		log.Println("[agent] registered successfully")
	}

	// 2. Heartbeat loop
	ticker := time.NewTicker(time.Duration(cfg.PollInterval) * time.Second)
	defer ticker.Stop()

	for range ticker.C {
		hb := monitor.Collect(cfg.NodeID)

		ack, err := grpcClient.Heartbeat(ctx, hb)
		if err != nil {
			log.Printf("[agent] heartbeat failed: %v", err)
			continue
		}

		if len(ack.Tasks) > 0 {
			log.Printf("[agent] received %d task(s)", len(ack.Tasks))
			handler.ProcessTasks(ack.Tasks, driver, func(result contracts.TaskResult) {
				if err := grpcClient.ReportTaskResult(ctx, result); err != nil {
					log.Printf("[agent] failed to report task result %s: %v", result.TaskID, err)
				}
			})
		}
	}
}
