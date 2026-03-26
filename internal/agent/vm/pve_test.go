package vm

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// ── Factory integration tests ──────────────────────────────────────────

// Verify PVEDriver satisfies the Hypervisor interface at compile time.
var _ Hypervisor = (*PVEDriver)(nil)

func TestFactory_PVE_MissingNode(t *testing.T) {
	// Should fail because "node" is required
	_, err := NewHypervisor(BackendPVE, map[string]string{
		"api_url":          "https://127.0.0.1:8006",
		"api_token_id":     "root@pam!test",
		"api_token_secret": "secret",
	})
	if err == nil {
		t.Fatal("expected error when node is missing")
	}
	if !strings.Contains(err.Error(), "node") {
		t.Fatalf("expected error about missing node, got: %v", err)
	}
}

func TestFactory_PVE_MissingAPIURL(t *testing.T) {
	_, err := NewHypervisor(BackendPVE, map[string]string{
		"node": "pve1",
	})
	if err == nil {
		t.Fatal("expected error when api_url is missing")
	}
	if !strings.Contains(err.Error(), "api_url") {
		t.Fatalf("expected error about missing api_url, got: %v", err)
	}
}

func TestFactory_PVE_InvalidTemplateVMID(t *testing.T) {
	_, err := NewHypervisor(BackendPVE, map[string]string{
		"api_url":          "https://127.0.0.1:8006",
		"api_token_id":     "root@pam!test",
		"api_token_secret": "secret",
		"node":             "pve1",
		"template_vmid":    "not-a-number",
	})
	if err == nil {
		t.Fatal("expected error for invalid template_vmid")
	}
}

func TestFactory_PVE_Success(t *testing.T) {
	h, err := NewHypervisor(BackendPVE, map[string]string{
		"api_url":          "https://127.0.0.1:8006",
		"api_token_id":     "root@pam!test",
		"api_token_secret": "secret",
		"node":             "pve1",
		"template_vmid":    "9000",
		"storage":          "local-lvm",
		"insecure":         "true",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if _, ok := h.(*PVEDriver); !ok {
		t.Fatalf("expected *PVEDriver, got %T", h)
	}
}

// ── PVE naming & helper tests ──────────────────────────────────────────

func TestPVEName(t *testing.T) {
	if got := pveName("abc-123"); got != "celeris-abc-123" {
		t.Fatalf("expected celeris-abc-123, got %s", got)
	}
}

func TestNormalizePVEState(t *testing.T) {
	tests := []struct {
		input    string
		expected string
	}{
		{"running", "running"},
		{"stopped", "stopped"},
		{"paused", "paused"},
		{"unknown-state", "unknown"},
		{"", "unknown"},
	}
	for _, tt := range tests {
		if got := normalizePVEState(tt.input); got != tt.expected {
			t.Errorf("normalizePVEState(%q) = %q, want %q", tt.input, got, tt.expected)
		}
	}
}

func TestDefaultGateway(t *testing.T) {
	tests := []struct {
		input    string
		expected string
	}{
		{"192.168.1.100", "192.168.1.1"},
		{"10.0.0.50", "10.0.0.1"},
		{"invalid", ""},
		{"", ""},
	}
	for _, tt := range tests {
		if got := defaultGateway(tt.input); got != tt.expected {
			t.Errorf("defaultGateway(%q) = %q, want %q", tt.input, got, tt.expected)
		}
	}
}

func TestResolveTemplate(t *testing.T) {
	d := &PVEDriver{templateID: 9000}

	// Numeric OS string → use as template VMID
	if got := d.resolveTemplate("1001"); got != 1001 {
		t.Fatalf("expected 1001, got %d", got)
	}

	// Non-numeric OS string → fall back to default
	if got := d.resolveTemplate("ubuntu-22.04"); got != 9000 {
		t.Fatalf("expected 9000 (default), got %d", got)
	}

	// Empty OS → fall back to default
	if got := d.resolveTemplate(""); got != 9000 {
		t.Fatalf("expected 9000 (default), got %d", got)
	}

	// No default and non-numeric → 0
	d2 := &PVEDriver{templateID: 0}
	if got := d2.resolveTemplate("ubuntu"); got != 0 {
		t.Fatalf("expected 0, got %d", got)
	}
}

// ── PVE Client HTTP tests ─────────────────────────────────────────────

func TestPVEClient_AuthHeader(t *testing.T) {
	var receivedAuth string
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		receivedAuth = r.Header.Get("Authorization")
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]interface{}{"data": "ok"})
	}))
	defer srv.Close()

	client, err := NewPVEClient(PVEClientConfig{
		APIURL:      srv.URL,
		TokenID:     "root@pam!agent",
		TokenSecret: "my-secret-123",
		Insecure:    true,
	})
	if err != nil {
		t.Fatalf("NewPVEClient: %v", err)
	}

	_, err = client.Get("/version", nil)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}

	expected := "PVEAPIToken=root@pam!agent=my-secret-123"
	if receivedAuth != expected {
		t.Fatalf("auth header = %q, want %q", receivedAuth, expected)
	}
}

func TestPVEClient_NextVMID(t *testing.T) {
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api2/json/cluster/nextid" {
			t.Fatalf("unexpected path: %s", r.URL.Path)
		}
		w.Header().Set("Content-Type", "application/json")
		// PVE returns nextid as a quoted string
		json.NewEncoder(w).Encode(map[string]interface{}{"data": "105"})
	}))
	defer srv.Close()

	client, _ := NewPVEClient(PVEClientConfig{
		APIURL:      srv.URL,
		TokenID:     "test@pam!t",
		TokenSecret: "s",
		Insecure:    true,
	})

	vmid, err := client.NextVMID()
	if err != nil {
		t.Fatalf("NextVMID: %v", err)
	}
	if vmid != 105 {
		t.Fatalf("expected VMID 105, got %d", vmid)
	}
}

func TestPVEClient_ListQEMU(t *testing.T) {
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api2/json/nodes/pve1/qemu" {
			t.Fatalf("unexpected path: %s", r.URL.Path)
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]interface{}{
			"data": []map[string]interface{}{
				{"vmid": 100, "name": "celeris-inst-1", "status": "running", "cpus": 2, "maxmem": 2147483648, "maxdisk": 42949672960},
				{"vmid": 101, "name": "other-vm", "status": "stopped", "cpus": 1, "maxmem": 1073741824, "maxdisk": 10737418240},
				{"vmid": 102, "name": "celeris-inst-2", "status": "stopped", "cpus": 4, "maxmem": 4294967296, "maxdisk": 85899345920},
			},
		})
	}))
	defer srv.Close()

	client, _ := NewPVEClient(PVEClientConfig{
		APIURL:      srv.URL,
		TokenID:     "test@pam!t",
		TokenSecret: "s",
		Insecure:    true,
	})

	vms, err := client.ListQEMU("pve1")
	if err != nil {
		t.Fatalf("ListQEMU: %v", err)
	}
	if len(vms) != 3 {
		t.Fatalf("expected 3 VMs, got %d", len(vms))
	}
}

func TestPVEDriver_List_FiltersCeleris(t *testing.T) {
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]interface{}{
			"data": []map[string]interface{}{
				{"vmid": 100, "name": "celeris-inst-1", "status": "running", "cpus": 2, "maxmem": 2147483648, "maxdisk": 42949672960},
				{"vmid": 101, "name": "other-vm", "status": "stopped", "cpus": 1, "maxmem": 1073741824, "maxdisk": 10737418240},
				{"vmid": 102, "name": "celeris-inst-2", "status": "stopped", "cpus": 4, "maxmem": 4294967296, "maxdisk": 85899345920},
			},
		})
	}))
	defer srv.Close()

	client, _ := NewPVEClient(PVEClientConfig{
		APIURL:      srv.URL,
		TokenID:     "test@pam!t",
		TokenSecret: "s",
		Insecure:    true,
	})

	driver := &PVEDriver{client: client, node: "pve1"}

	list, err := driver.List()
	if err != nil {
		t.Fatalf("List: %v", err)
	}

	// Should only return celeris-managed VMs (2 out of 3)
	if len(list) != 2 {
		t.Fatalf("expected 2 Celeris VMs, got %d", len(list))
	}

	// Check the first VM
	if list[0].InstanceID != "inst-1" {
		t.Fatalf("expected inst-1, got %s", list[0].InstanceID)
	}
	if list[0].State != "running" {
		t.Fatalf("expected running, got %s", list[0].State)
	}
	if list[0].CPU != 2 {
		t.Fatalf("expected 2 CPUs, got %d", list[0].CPU)
	}

	// Check the second VM
	if list[1].InstanceID != "inst-2" {
		t.Fatalf("expected inst-2, got %s", list[1].InstanceID)
	}
	if list[1].State != "stopped" {
		t.Fatalf("expected stopped, got %s", list[1].State)
	}
}

func TestPVEDriver_FindVMID(t *testing.T) {
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]interface{}{
			"data": []map[string]interface{}{
				{"vmid": 100, "name": "celeris-inst-1", "status": "running"},
				{"vmid": 101, "name": "other-vm", "status": "stopped"},
				{"vmid": 102, "name": "celeris-inst-2", "status": "stopped"},
			},
		})
	}))
	defer srv.Close()

	client, _ := NewPVEClient(PVEClientConfig{
		APIURL:      srv.URL,
		TokenID:     "test@pam!t",
		TokenSecret: "s",
		Insecure:    true,
	})

	driver := &PVEDriver{client: client, node: "pve1"}

	// Found
	vmid, err := driver.findVMID("inst-1")
	if err != nil {
		t.Fatalf("findVMID inst-1: %v", err)
	}
	if vmid != 100 {
		t.Fatalf("expected VMID 100, got %d", vmid)
	}

	// Found (second)
	vmid, err = driver.findVMID("inst-2")
	if err != nil {
		t.Fatalf("findVMID inst-2: %v", err)
	}
	if vmid != 102 {
		t.Fatalf("expected VMID 102, got %d", vmid)
	}

	// Not found
	_, err = driver.findVMID("nonexistent")
	if err == nil {
		t.Fatal("expected error for nonexistent VM")
	}
}

func TestPVEDriver_Start(t *testing.T) {
	callCount := 0
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		callCount++
		w.Header().Set("Content-Type", "application/json")

		switch {
		// First call: list VMs to find VMID
		case r.URL.Path == "/api2/json/nodes/pve1/qemu" && r.Method == "GET":
			json.NewEncoder(w).Encode(map[string]interface{}{
				"data": []map[string]interface{}{
					{"vmid": 100, "name": "celeris-test-vm", "status": "stopped"},
				},
			})
		// Second call: start the VM
		case r.URL.Path == "/api2/json/nodes/pve1/qemu/100/status/start" && r.Method == "POST":
			json.NewEncoder(w).Encode(map[string]interface{}{
				"data": "UPID:pve1:00001234:12345678:12345678:qmstart:100:root@pam:",
			})
		// Third call: check task status
		case strings.Contains(r.URL.Path, "/tasks/") && strings.Contains(r.URL.Path, "/status"):
			json.NewEncoder(w).Encode(map[string]interface{}{
				"data": map[string]interface{}{
					"status":     "stopped",
					"exitstatus": "OK",
				},
			})
		default:
			t.Logf("unexpected request: %s %s", r.Method, r.URL.Path)
			http.Error(w, "not found", 404)
		}
	}))
	defer srv.Close()

	client, _ := NewPVEClient(PVEClientConfig{
		APIURL:      srv.URL,
		TokenID:     "test@pam!t",
		TokenSecret: "s",
		Insecure:    true,
	})

	driver := &PVEDriver{client: client, node: "pve1"}

	err := driver.Start("test-vm")
	if err != nil {
		t.Fatalf("Start: %v", err)
	}

	if callCount < 3 {
		t.Fatalf("expected at least 3 API calls (list, start, task-status), got %d", callCount)
	}
}

func TestPVEDriver_Stop(t *testing.T) {
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")

		switch {
		case r.URL.Path == "/api2/json/nodes/pve1/qemu" && r.Method == "GET":
			json.NewEncoder(w).Encode(map[string]interface{}{
				"data": []map[string]interface{}{
					{"vmid": 100, "name": "celeris-test-vm", "status": "running"},
				},
			})
		case strings.Contains(r.URL.Path, "/status/shutdown") && r.Method == "POST":
			json.NewEncoder(w).Encode(map[string]interface{}{
				"data": "UPID:pve1:00001234:12345678:12345678:qmshutdown:100:root@pam:",
			})
		case strings.Contains(r.URL.Path, "/tasks/") && strings.Contains(r.URL.Path, "/status"):
			json.NewEncoder(w).Encode(map[string]interface{}{
				"data": map[string]interface{}{
					"status":     "stopped",
					"exitstatus": "OK",
				},
			})
		default:
			fmt.Fprintf(w, `{"data": null}`)
		}
	}))
	defer srv.Close()

	client, _ := NewPVEClient(PVEClientConfig{
		APIURL:      srv.URL,
		TokenID:     "test@pam!t",
		TokenSecret: "s",
		Insecure:    true,
	})

	driver := &PVEDriver{client: client, node: "pve1"}

	err := driver.Stop("test-vm")
	if err != nil {
		t.Fatalf("Stop: %v", err)
	}
}

func TestPVEClient_ErrorHandling(t *testing.T) {
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, `{"errors": {"vmid": "VM 999 does not exist"}}`, 500)
	}))
	defer srv.Close()

	client, _ := NewPVEClient(PVEClientConfig{
		APIURL:      srv.URL,
		TokenID:     "test@pam!t",
		TokenSecret: "s",
		Insecure:    true,
	})

	_, err := client.GetQEMUStatus("pve1", 999)
	if err == nil {
		t.Fatal("expected error for non-existent VM")
	}
	if !strings.Contains(err.Error(), "500") {
		t.Fatalf("expected HTTP 500 in error, got: %v", err)
	}
}
