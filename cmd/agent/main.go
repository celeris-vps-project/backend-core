package main

import (
	"backend-core/internal/agent/client"
	"backend-core/internal/agent/config"
	"backend-core/internal/agent/handler"
	"backend-core/internal/agent/nat"
	"backend-core/internal/agent/monitor"
	"backend-core/internal/agent/vm"
	"backend-core/pkg/contracts"
	"context"
	"flag"
	"fmt"
	"log"
	"os"
	"time"

	"gopkg.in/yaml.v3"
)

var version = "dev"

// nodeCredential is the structure persisted in the credential file.
type nodeCredential struct {
	NodeID    string `yaml:"node_id"`
	NodeToken string `yaml:"node_token"`
}

func main() {
	log.Printf("[agent] Celeris Agent %s", version)

	cfgPath := flag.String("config", "agent.yaml", "path to agent YAML config file")
	flag.Parse()

	// Load config from YAML file; fall back to defaults + env overrides if file not found
	cfg, err := config.LoadFromFile(*cfgPath)
	if err != nil {
		log.Printf("[agent] could not load config file %s: %v (using defaults + env)", *cfgPath, err)
		cfg = config.DefaultConfig()
	}

	// Environment variables override YAML values when set
	if v := os.Getenv("AGENT_BOOTSTRAP_TOKEN"); v != "" {
		cfg.BootstrapToken = v
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
	// PVE environment variable overrides
	if v := os.Getenv("AGENT_PVE_API_URL"); v != "" {
		cfg.VirtOpts["api_url"] = v
	}
	if v := os.Getenv("AGENT_PVE_TOKEN_ID"); v != "" {
		cfg.VirtOpts["api_token_id"] = v
	}
	if v := os.Getenv("AGENT_PVE_TOKEN_SECRET"); v != "" {
		cfg.VirtOpts["api_token_secret"] = v
	}
	if v := os.Getenv("AGENT_PVE_NODE"); v != "" {
		cfg.VirtOpts["node"] = v
	}
	if v := os.Getenv("AGENT_PVE_INSECURE"); v != "" {
		cfg.VirtOpts["insecure"] = v
	}
	if v := os.Getenv("AGENT_PVE_TEMPLATE_VMID"); v != "" {
		cfg.VirtOpts["template_vmid"] = v
	}
	if v := os.Getenv("AGENT_PVE_STORAGE"); v != "" {
		cfg.VirtOpts["storage"] = v
	}

	driver, err := vm.NewHypervisor(vm.Backend(cfg.VirtBackend), cfg.VirtOpts)
	if err != nil {
		log.Fatalf("[agent] failed to create hypervisor: %v", err)
	}
	natForwarder := nat.NewIPTablesForwarder(cfg.NAT)

	// Connect to the controller via gRPC
	grpcClient, err := client.Dial(cfg.GRPCAddress)
	if err != nil {
		log.Fatalf("[agent] failed to connect to controller: %v", err)
	}
	defer grpcClient.Close()

	log.Printf("[agent] starting grpc=%s backend=%s", cfg.GRPCAddress, cfg.VirtBackend)

	ctx := context.Background()

	// ── Credential bootstrap flow ──────────────────────────────────────
	// 1. Try to load an existing node credential from the credential file.
	// 2. If not found, perform bootstrap registration with the one-time token.
	// 3. The server determines NodeID from the bootstrap token — no need to
	//    configure node_id manually.
	credFile := cfg.CredentialFile
	if credFile == "" {
		credFile = "node-credential.yaml"
	}

	var nodeID string

	if cred, err := loadCredential(credFile); err == nil && cred.NodeToken != "" {
		// Existing credential found — skip registration
		grpcClient.SetNodeToken(cred.NodeToken)
		nodeID = cred.NodeID
		log.Printf("[agent] loaded existing credential from %s (node=%s)", credFile, nodeID)
	} else {
		// No credential — must bootstrap
		if cfg.BootstrapToken == "" {
			log.Fatalf("[agent] no credential file and no bootstrap_token configured — cannot register")
		}
		log.Printf("[agent] no credential found, bootstrapping with token...")

		hostname, _ := os.Hostname()
		reg := contracts.AgentRegistration{
			BootstrapToken: cfg.BootstrapToken,
			Hostname:       hostname,
			IP:             "127.0.0.1",
			Version:        version,
		}
		result, err := grpcClient.Register(ctx, reg)
		if err != nil {
			log.Fatalf("[agent] bootstrap registration failed: %v", err)
		}
		nodeID = result.NodeID
		log.Printf("[agent] bootstrap registration successful, assigned node=%s", nodeID)

		if err := saveCredential(credFile, nodeCredential{NodeID: nodeID, NodeToken: result.NodeToken}); err != nil {
			log.Fatalf("[agent] failed to save credential to %s: %v", credFile, err)
		}
		log.Printf("[agent] node credential saved to %s", credFile)
	}

	collector := monitor.NewCollector(nodeID, driver)

	// ── Heartbeat loop ─────────────────────────────────────────────────
	ticker := time.NewTicker(time.Duration(cfg.PollInterval) * time.Second)
	defer ticker.Stop()

	for range ticker.C {
		hb := collector.Collect()

		ack, err := grpcClient.Heartbeat(ctx, hb)
		if err != nil {
			log.Printf("[agent] heartbeat failed: %v", err)
			continue
		}

		if len(ack.Tasks) > 0 {
			log.Printf("[agent] received %d task(s)", len(ack.Tasks))
			handler.ProcessTasks(ack.Tasks, driver, natForwarder, func(result contracts.TaskResult) {
				if err := grpcClient.ReportTaskResult(ctx, result); err != nil {
					log.Printf("[agent] failed to report task result %s: %v", result.TaskID, err)
				}
			})
		}
	}
}

// ── Credential file helpers ────────────────────────────────────────────

func loadCredential(path string) (nodeCredential, error) {
	var cred nodeCredential
	data, err := os.ReadFile(path)
	if err != nil {
		return cred, fmt.Errorf("read credential file: %w", err)
	}
	if err := yaml.Unmarshal(data, &cred); err != nil {
		return cred, fmt.Errorf("parse credential file: %w", err)
	}
	return cred, nil
}

func saveCredential(path string, cred nodeCredential) error {
	data, err := yaml.Marshal(&cred)
	if err != nil {
		return fmt.Errorf("marshal credential: %w", err)
	}
	return os.WriteFile(path, data, 0600)
}
