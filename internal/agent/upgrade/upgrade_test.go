package upgrade

import (
	"context"
	"testing"
)

func TestGetLastest(t *testing.T) {
	upgrader := NewUpgrader(
		"https://api.github.com/repos/celeris-vps-project/backend-core/releases/latest",
		"v0.0.67",
		"/bin/sh",
		"-c",
		"curl -fsSL https://github.com/celeris-vps-project/backend-core/releases/download/{version}/celeris-agent-linux-amd64 && systemctl restart celeris-agent",
	)
	tag, err := upgrader.GetLatest(context.Background())
	if err != nil {
		return
	}
	if tag != "v0.0.67" {
		t.Errorf("Latest tag is: %s", tag)
	}
}
