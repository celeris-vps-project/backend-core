package vm

import (
	"crypto/tls"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// PVEClient is a lightweight HTTP client for the Proxmox VE REST API.
//
// Authentication uses API tokens (PVEAPIToken header), which is the
// recommended approach for automated/service access. API tokens avoid
// the need for ticket-based session management.
//
// Reference: https://pve.proxmox.com/pve-docs/api-viewer/
type PVEClient struct {
	baseURL     string // e.g. "https://127.0.0.1:8006"
	tokenID     string // e.g. "root@pam!celeris"
	tokenSecret string // the API token secret
	httpClient  *http.Client
}

// PVEClientConfig holds the configuration for creating a PVEClient.
type PVEClientConfig struct {
	APIURL      string // e.g. "https://127.0.0.1:8006"
	TokenID     string // e.g. "root@pam!celeris"
	TokenSecret string
	Insecure    bool          // skip TLS certificate verification (for self-signed certs)
	Timeout     time.Duration // HTTP request timeout; defaults to 30s
}

// NewPVEClient creates a new Proxmox VE API client.
func NewPVEClient(cfg PVEClientConfig) (*PVEClient, error) {
	if cfg.APIURL == "" {
		return nil, fmt.Errorf("pve: api_url is required")
	}
	if cfg.TokenID == "" {
		return nil, fmt.Errorf("pve: api_token_id is required")
	}
	if cfg.TokenSecret == "" {
		return nil, fmt.Errorf("pve: api_token_secret is required")
	}

	timeout := cfg.Timeout
	if timeout == 0 {
		timeout = 30 * time.Second
	}

	transport := &http.Transport{
		TLSClientConfig: &tls.Config{
			InsecureSkipVerify: cfg.Insecure,
		},
	}

	return &PVEClient{
		baseURL:     strings.TrimRight(cfg.APIURL, "/"),
		tokenID:     cfg.TokenID,
		tokenSecret: cfg.TokenSecret,
		httpClient: &http.Client{
			Timeout:   timeout,
			Transport: transport,
		},
	}, nil
}

// ── Generic request helpers ────────────────────────────────────────────

// pveResponse is the top-level JSON envelope returned by every PVE API call.
type pveResponse struct {
	Data   json.RawMessage `json:"data"`
	Errors json.RawMessage `json:"errors,omitempty"`
}

// do executes an authenticated HTTP request against the PVE API.
// path is relative to /api2/json, e.g. "/nodes/pve1/qemu".
// body is optional form-encoded data for POST/PUT.
func (c *PVEClient) do(method, path string, params map[string]string) (json.RawMessage, error) {
	fullURL := c.baseURL + "/api2/json" + path

	var body io.Reader
	if params != nil && (method == http.MethodPost || method == http.MethodPut) {
		form := url.Values{}
		for k, v := range params {
			form.Set(k, v)
		}
		body = strings.NewReader(form.Encode())
	}

	// For GET/DELETE with params, append as query string
	if params != nil && (method == http.MethodGet || method == http.MethodDelete) {
		u, err := url.Parse(fullURL)
		if err != nil {
			return nil, fmt.Errorf("pve: parse url: %w", err)
		}
		q := u.Query()
		for k, v := range params {
			q.Set(k, v)
		}
		u.RawQuery = q.Encode()
		fullURL = u.String()
	}

	req, err := http.NewRequest(method, fullURL, body)
	if err != nil {
		return nil, fmt.Errorf("pve: create request: %w", err)
	}

	// PVE API Token authentication header
	req.Header.Set("Authorization", fmt.Sprintf("PVEAPIToken=%s=%s", c.tokenID, c.tokenSecret))
	if body != nil {
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	}

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("pve: request %s %s: %w", method, path, err)
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("pve: read response: %w", err)
	}

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("pve: %s %s returned %d: %s", method, path, resp.StatusCode, string(respBody))
	}

	var pveResp pveResponse
	if err := json.Unmarshal(respBody, &pveResp); err != nil {
		return nil, fmt.Errorf("pve: decode response: %w", err)
	}

	return pveResp.Data, nil
}

// Get performs an authenticated GET request.
func (c *PVEClient) Get(path string, params map[string]string) (json.RawMessage, error) {
	return c.do(http.MethodGet, path, params)
}

// Post performs an authenticated POST request.
func (c *PVEClient) Post(path string, params map[string]string) (json.RawMessage, error) {
	return c.do(http.MethodPost, path, params)
}

// Put performs an authenticated PUT request.
func (c *PVEClient) Put(path string, params map[string]string) (json.RawMessage, error) {
	return c.do(http.MethodPut, path, params)
}

// Delete performs an authenticated DELETE request.
func (c *PVEClient) Delete(path string, params map[string]string) (json.RawMessage, error) {
	return c.do(http.MethodDelete, path, params)
}

// ── PVE-specific helpers ───────────────────────────────────────────────

// NextVMID retrieves the next available VMID from the cluster.
// PVE API: GET /cluster/nextid
func (c *PVEClient) NextVMID() (int, error) {
	data, err := c.Get("/cluster/nextid", nil)
	if err != nil {
		return 0, fmt.Errorf("pve nextid: %w", err)
	}

	// data is a JSON string like "100"
	var vmid int
	// Try parsing as int first (some PVE versions return bare int)
	if err := json.Unmarshal(data, &vmid); err == nil {
		return vmid, nil
	}
	// Try parsing as quoted string
	var vmidStr string
	if err := json.Unmarshal(data, &vmidStr); err == nil {
		var id int
		if _, err := fmt.Sscanf(vmidStr, "%d", &id); err == nil {
			return id, nil
		}
	}
	return 0, fmt.Errorf("pve nextid: unexpected response: %s", string(data))
}

// pveVMInfo represents the essential fields from a PVE VM status query.
type pveVMInfo struct {
	VMID        int     `json:"vmid"`
	Name        string  `json:"name"`
	Status      string  `json:"status"` // "running", "stopped", etc.
	Description string  `json:"description,omitempty"`
	CPUs        int     `json:"cpus"`
	MaxMem      int64   `json:"maxmem"` // bytes
	MaxDisk     int64   `json:"maxdisk"`
	PID         int     `json:"pid,omitempty"`
	Uptime      int64   `json:"uptime"`
	NetIn       float64 `json:"netin"`
	NetOut      float64 `json:"netout"`
}

// ListQEMU lists all QEMU VMs on the specified node.
// PVE API: GET /nodes/{node}/qemu
func (c *PVEClient) ListQEMU(node string) ([]pveVMInfo, error) {
	data, err := c.Get(fmt.Sprintf("/nodes/%s/qemu", node), nil)
	if err != nil {
		return nil, fmt.Errorf("pve list qemu: %w", err)
	}

	var vms []pveVMInfo
	if err := json.Unmarshal(data, &vms); err != nil {
		return nil, fmt.Errorf("pve list qemu decode: %w", err)
	}
	return vms, nil
}

// GetQEMUStatus retrieves the current status of a QEMU VM.
// PVE API: GET /nodes/{node}/qemu/{vmid}/status/current
func (c *PVEClient) GetQEMUStatus(node string, vmid int) (*pveVMInfo, error) {
	data, err := c.Get(fmt.Sprintf("/nodes/%s/qemu/%d/status/current", node, vmid), nil)
	if err != nil {
		return nil, fmt.Errorf("pve qemu status: %w", err)
	}

	var info pveVMInfo
	if err := json.Unmarshal(data, &info); err != nil {
		return nil, fmt.Errorf("pve qemu status decode: %w", err)
	}
	return &info, nil
}

// GetQEMUConfig retrieves the configuration of a QEMU VM.
// PVE API: GET /nodes/{node}/qemu/{vmid}/config
func (c *PVEClient) GetQEMUConfig(node string, vmid int) (map[string]interface{}, error) {
	data, err := c.Get(fmt.Sprintf("/nodes/%s/qemu/%d/config", node, vmid), nil)
	if err != nil {
		return nil, fmt.Errorf("pve qemu config: %w", err)
	}

	var config map[string]interface{}
	if err := json.Unmarshal(data, &config); err != nil {
		return nil, fmt.Errorf("pve qemu config decode: %w", err)
	}
	return config, nil
}

// CloneQEMU clones a template VM to a new VM.
// PVE API: POST /nodes/{node}/qemu/{templateVMID}/clone
// Returns the task UPID for tracking.
func (c *PVEClient) CloneQEMU(node string, templateVMID, newVMID int, params map[string]string) (string, error) {
	path := fmt.Sprintf("/nodes/%s/qemu/%d/clone", node, templateVMID)

	if params == nil {
		params = map[string]string{}
	}
	params["newid"] = fmt.Sprintf("%d", newVMID)

	data, err := c.Post(path, params)
	if err != nil {
		return "", fmt.Errorf("pve clone: %w", err)
	}

	// data is the UPID string
	var upid string
	if err := json.Unmarshal(data, &upid); err != nil {
		return "", fmt.Errorf("pve clone decode upid: %w", err)
	}
	return upid, nil
}

// ResizeQEMUDisk resizes a disk on a QEMU VM.
// PVE API: PUT /nodes/{node}/qemu/{vmid}/resize
func (c *PVEClient) ResizeQEMUDisk(node string, vmid int, disk, size string) error {
	path := fmt.Sprintf("/nodes/%s/qemu/%d/resize", node, vmid)
	_, err := c.Put(path, map[string]string{
		"disk": disk,
		"size": size,
	})
	if err != nil {
		return fmt.Errorf("pve resize disk: %w", err)
	}
	return nil
}

// SetQEMUConfig updates the configuration of a QEMU VM.
// PVE API: PUT /nodes/{node}/qemu/{vmid}/config
func (c *PVEClient) SetQEMUConfig(node string, vmid int, params map[string]string) error {
	path := fmt.Sprintf("/nodes/%s/qemu/%d/config", node, vmid)
	_, err := c.Put(path, params)
	if err != nil {
		return fmt.Errorf("pve set config: %w", err)
	}
	return nil
}

// StartQEMU starts a stopped QEMU VM.
// PVE API: POST /nodes/{node}/qemu/{vmid}/status/start
func (c *PVEClient) StartQEMU(node string, vmid int) (string, error) {
	data, err := c.Post(fmt.Sprintf("/nodes/%s/qemu/%d/status/start", node, vmid), nil)
	if err != nil {
		return "", fmt.Errorf("pve start: %w", err)
	}

	var upid string
	if err := json.Unmarshal(data, &upid); err != nil {
		return "", fmt.Errorf("pve start decode upid: %w", err)
	}
	return upid, nil
}

// StopQEMU stops a running QEMU VM.
// PVE API: POST /nodes/{node}/qemu/{vmid}/status/stop
func (c *PVEClient) StopQEMU(node string, vmid int) (string, error) {
	data, err := c.Post(fmt.Sprintf("/nodes/%s/qemu/%d/status/stop", node, vmid), nil)
	if err != nil {
		return "", fmt.Errorf("pve stop: %w", err)
	}

	var upid string
	if err := json.Unmarshal(data, &upid); err != nil {
		return "", fmt.Errorf("pve stop decode upid: %w", err)
	}
	return upid, nil
}

// ShutdownQEMU gracefully shuts down a running QEMU VM via ACPI.
// PVE API: POST /nodes/{node}/qemu/{vmid}/status/shutdown
func (c *PVEClient) ShutdownQEMU(node string, vmid int, timeout int) (string, error) {
	params := map[string]string{}
	if timeout > 0 {
		params["timeout"] = fmt.Sprintf("%d", timeout)
	}
	data, err := c.Post(fmt.Sprintf("/nodes/%s/qemu/%d/status/shutdown", node, vmid), params)
	if err != nil {
		return "", fmt.Errorf("pve shutdown: %w", err)
	}

	var upid string
	if err := json.Unmarshal(data, &upid); err != nil {
		return "", fmt.Errorf("pve shutdown decode upid: %w", err)
	}
	return upid, nil
}

// RebootQEMU reboots a running QEMU VM.
// PVE API: POST /nodes/{node}/qemu/{vmid}/status/reboot
func (c *PVEClient) RebootQEMU(node string, vmid int, timeout int) (string, error) {
	params := map[string]string{}
	if timeout > 0 {
		params["timeout"] = fmt.Sprintf("%d", timeout)
	}
	data, err := c.Post(fmt.Sprintf("/nodes/%s/qemu/%d/status/reboot", node, vmid), params)
	if err != nil {
		return "", fmt.Errorf("pve reboot: %w", err)
	}

	var upid string
	if err := json.Unmarshal(data, &upid); err != nil {
		return "", fmt.Errorf("pve reboot decode upid: %w", err)
	}
	return upid, nil
}

// DeleteQEMU removes a QEMU VM.
// PVE API: DELETE /nodes/{node}/qemu/{vmid}
func (c *PVEClient) DeleteQEMU(node string, vmid int, params map[string]string) (string, error) {
	data, err := c.Delete(fmt.Sprintf("/nodes/%s/qemu/%d", node, vmid), params)
	if err != nil {
		return "", fmt.Errorf("pve delete: %w", err)
	}

	var upid string
	if err := json.Unmarshal(data, &upid); err != nil {
		return "", fmt.Errorf("pve delete decode upid: %w", err)
	}
	return upid, nil
}

// ── Task waiting ───────────────────────────────────────────────────────

// pveTaskStatus holds the fields from a PVE task status query.
type pveTaskStatus struct {
	Status     string `json:"status"`     // "running", "stopped"
	ExitStatus string `json:"exitstatus"` // "OK" on success, error message on failure
	Type       string `json:"type"`
	ID         string `json:"id"`
	Node       string `json:"node"`
	UPID       string `json:"upid"`
}

// WaitForTask polls a PVE task until it completes or the timeout expires.
// PVE API: GET /nodes/{node}/tasks/{upid}/status
//
// The UPID encodes the node name, so the node parameter must match.
func (c *PVEClient) WaitForTask(node, upid string, timeout time.Duration) error {
	if timeout == 0 {
		timeout = 120 * time.Second
	}

	deadline := time.Now().Add(timeout)
	pollInterval := 1 * time.Second

	for time.Now().Before(deadline) {
		data, err := c.Get(fmt.Sprintf("/nodes/%s/tasks/%s/status", node, url.PathEscape(upid)), nil)
		if err != nil {
			return fmt.Errorf("pve wait task: %w", err)
		}

		var status pveTaskStatus
		if err := json.Unmarshal(data, &status); err != nil {
			return fmt.Errorf("pve wait task decode: %w", err)
		}

		if status.Status == "stopped" {
			if status.ExitStatus == "OK" {
				return nil
			}
			return fmt.Errorf("pve task failed: %s (exit=%s)", upid, status.ExitStatus)
		}

		time.Sleep(pollInterval)
		// Back off slightly for long-running tasks
		if pollInterval < 5*time.Second {
			pollInterval = pollInterval * 3 / 2
		}
	}

	return fmt.Errorf("pve task timeout after %v: %s", timeout, upid)
}

// ── Network info helpers ───────────────────────────────────────────────

// pveAgentNetIface represents a network interface as reported by the QEMU guest agent.
type pveAgentNetIface struct {
	Name        string `json:"name"`
	HWAddr      string `json:"hardware-address"`
	IPAddresses []struct {
		Type    string `json:"ip-address-type"` // "ipv4" or "ipv6"
		Address string `json:"ip-address"`
		Prefix  int    `json:"prefix"`
	} `json:"ip-addresses"`
}

// GetQEMUAgentNetworkInterfaces queries the QEMU guest agent for network info.
// Requires qemu-guest-agent running inside the VM.
// PVE API: GET /nodes/{node}/qemu/{vmid}/agent/network-get-interfaces
func (c *PVEClient) GetQEMUAgentNetworkInterfaces(node string, vmid int) ([]pveAgentNetIface, error) {
	data, err := c.Get(fmt.Sprintf("/nodes/%s/qemu/%d/agent/network-get-interfaces", node, vmid), nil)
	if err != nil {
		return nil, fmt.Errorf("pve agent network: %w", err)
	}

	// PVE wraps the result in {"result": [...]}
	var wrapper struct {
		Result []pveAgentNetIface `json:"result"`
	}
	if err := json.Unmarshal(data, &wrapper); err != nil {
		// Try direct array (some PVE versions)
		var ifaces []pveAgentNetIface
		if err2 := json.Unmarshal(data, &ifaces); err2 != nil {
			return nil, fmt.Errorf("pve agent network decode: %w", err)
		}
		return ifaces, nil
	}
	return wrapper.Result, nil
}
