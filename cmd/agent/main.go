package main

import (
	"backend-core/internal/agent/config"
	"backend-core/internal/agent/handler"
	"backend-core/internal/agent/monitor"
	"backend-core/internal/agent/vm"
	"backend-core/pkg/contracts"
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"time"
)

func main() {
	cfg := config.DefaultConfig()
	cfg.NodeID = envOrDefault("AGENT_NODE_ID", "node-1")
	cfg.Secret = envOrDefault("AGENT_SECRET", "changeme")
	cfg.ControllerURL = envOrDefault("AGENT_CONTROLLER_URL", "http://127.0.0.1:8888")
	cfg.VirtBackend = envOrDefault("AGENT_VIRT_BACKEND", "stub")

	// Backend-specific options from env
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

	log.Printf("[agent] starting node=%s controller=%s backend=%s", cfg.NodeID, cfg.ControllerURL, cfg.VirtBackend)

	// 1. Register with the controller
	reg := contracts.AgentRegistration{
		NodeID:   cfg.NodeID,
		Secret:   cfg.Secret,
		Hostname: cfg.NodeID,
		IP:       "127.0.0.1",
		Version:  "v0.1.0",
	}
	if err := postJSON(cfg.ControllerURL+"/api/v1/agent/register", reg, nil); err != nil {
		log.Printf("[agent] registration failed (will retry on heartbeat): %v", err)
	} else {
		log.Println("[agent] registered successfully")
	}

	// 2. Heartbeat loop
	ticker := time.NewTicker(time.Duration(cfg.PollInterval) * time.Second)
	defer ticker.Stop()

	for range ticker.C {
		hb := monitor.Collect(cfg.NodeID)

		var ack contracts.HeartbeatAck
		if err := postJSON(cfg.ControllerURL+"/api/v1/agent/heartbeat", hb, &ack); err != nil {
			log.Printf("[agent] heartbeat failed: %v", err)
			continue
		}

		if len(ack.Tasks) > 0 {
			log.Printf("[agent] received %d task(s)", len(ack.Tasks))
			handler.ProcessTasks(ack.Tasks, driver, func(result contracts.TaskResult) {
				if err := postJSON(cfg.ControllerURL+"/api/v1/agent/tasks/result", result, nil); err != nil {
					log.Printf("[agent] failed to report task result %s: %v", result.TaskID, err)
				}
			})
		}
	}
}

func envOrDefault(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

func postJSON(url string, body interface{}, out interface{}) error {
	data, err := json.Marshal(body)
	if err != nil {
		return err
	}
	resp, err := http.Post(url, "application/json", bytes.NewReader(data))
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	respBody, _ := io.ReadAll(resp.Body)
	if resp.StatusCode >= 400 {
		return fmt.Errorf("HTTP %d: %s", resp.StatusCode, string(respBody))
	}
	if out != nil {
		return json.Unmarshal(respBody, out)
	}
	return nil
}
