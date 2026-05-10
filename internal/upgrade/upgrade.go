package upgrade

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"strings"
	"sync"
	"time"
)

var client = &http.Client{
	Timeout: 15 * time.Second,
}

type Upgrader struct {
	repoBase       string
	currentVersion string
	interval       time.Duration

	cmdName string
	cmdArgs []string

	mu        sync.Mutex
	upgrading bool
}

type Response struct {
	TagName string `json:"tag_name"`
}

// repoBase 示例：
// https://api.github.com/repos/OWNER/REPO/releases/latest
func NewUpgrader(repoBase, currentVersion, cmdName string, cmdArgs ...string) *Upgrader {
	return &Upgrader{
		repoBase:       repoBase,
		currentVersion: currentVersion,
		interval:       5 * time.Minute,
		cmdName:        cmdName,
		cmdArgs:        cmdArgs,
	}
}

func (u *Upgrader) GetLatest(ctx context.Context) (string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.repoBase, nil)
	if err != nil {
		return "", err
	}

	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("User-Agent", "go-upgrader")

	resp, err := client.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return "", fmt.Errorf("github api error: %s, body: %s", resp.Status, string(body))
	}

	var response Response
	if err := json.NewDecoder(resp.Body).Decode(&response); err != nil {
		return "", err
	}

	if response.TagName == "" {
		return "", fmt.Errorf("empty tag_name from github")
	}

	return response.TagName, nil
}

// StartUpgradeLoop 每隔 interval 检查一次版本
func (u *Upgrader) StartUpgradeLoop(ctx context.Context) error {
	if u.interval <= 0 {
		u.interval = 5 * time.Minute
	}

	go func() {
		ticker := time.NewTicker(u.interval)
		defer ticker.Stop()

		// 启动后先检查一次
		u.checkAndUpgrade(ctx)

		for {
			select {
			case <-ctx.Done():
				return

			case <-ticker.C:
				u.checkAndUpgrade(ctx)
			}
		}
	}()

	return nil
}

func (u *Upgrader) checkAndUpgrade(ctx context.Context) {
	defer func() {
		if r := recover(); r != nil {
			fmt.Println("upgrade panic recovered:", r)
		}
	}()

	latest, err := u.GetLatest(ctx)
	if err != nil {
		fmt.Println("get latest version failed:", err)
		return
	}

	if sameVersion(latest, u.currentVersion) {
		return
	}

	fmt.Printf("found new version: current=%s latest=%s\n", u.currentVersion, latest)

	if err := u.Upgrade(ctx, latest); err != nil {
		fmt.Println("upgrade failed:", err)
		return
	}

	u.currentVersion = latest
	fmt.Println("upgrade success:", latest)
}

func (u *Upgrader) Upgrade(ctx context.Context, latest string) error {
	u.mu.Lock()
	if u.upgrading {
		u.mu.Unlock()
		return fmt.Errorf("upgrade already running")
	}
	u.upgrading = true
	u.mu.Unlock()

	defer func() {
		u.mu.Lock()
		u.upgrading = false
		u.mu.Unlock()
	}()

	if u.cmdName == "" {
		return fmt.Errorf("empty upgrade command")
	}

	args := append([]string(nil), u.cmdArgs...)

	// 支持在命令参数里写 {version}
	for i := range args {
		args[i] = strings.ReplaceAll(args[i], "{version}", latest)
	}

	fmt.Printf("run upgrade command: %s %s\n", u.cmdName, strings.Join(args, " "))

	cmd := exec.CommandContext(ctx, u.cmdName, args...)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr

	return cmd.Run()
}

func sameVersion(a, b string) bool {
	a = strings.TrimSpace(a)
	b = strings.TrimSpace(b)

	a = strings.TrimPrefix(a, "v")
	b = strings.TrimPrefix(b, "v")

	return a == b
}
