package bifrost

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

func PidPath() string { return filepath.Join(Home(), "bifrost.pid") }
func LogPath() string { return filepath.Join(Home(), "bifrost.log") }

// PortOf is the port in a gateway URL such as http://localhost:8081.
func PortOf(gatewayURL string) int {
	u, err := url.Parse(gatewayURL)
	if err != nil {
		return 8080
	}
	if p, err := strconv.Atoi(u.Port()); err == nil {
		return p
	}
	return 8080
}

// Start launches the gateway in the background, bound to this machine only, and waits until it answers.
func Start(ctx context.Context, gatewayURL string) error {
	if !Managed() {
		return errNotManaged
	}
	if Healthy(gatewayURL) {
		return nil
	}
	bin := BinaryPath()
	if _, err := os.Stat(bin); err != nil {
		return fmt.Errorf("Bifrost binary missing at %s: run `slendertoken setup`", bin)
	}
	logf, err := os.OpenFile(LogPath(), os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		return err
	}
	defer logf.Close()
	cmd := exec.Command(bin, "-app-dir", DataDir(), "-host", "127.0.0.1", "-port", strconv.Itoa(PortOf(gatewayURL)))
	// relative paths inside the gateway (its databases) resolve here, not in whatever folder slendertoken was run from
	cmd.Dir = DataDir()
	cmd.Stdout, cmd.Stderr = logf, logf
	detach(cmd)
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("cannot start Bifrost: %w", err)
	}
	_ = os.WriteFile(PidPath(), []byte(strconv.Itoa(cmd.Process.Pid)), 0o600)
	_ = cmd.Process.Release()
	return waitHealthy(ctx, gatewayURL, 60*time.Second)
}

// Stop ends the gateway that Start launched.
func Stop() error {
	data, err := os.ReadFile(PidPath())
	if err != nil {
		return errors.New("no gateway started by slendertoken is recorded")
	}
	pid, err := strconv.Atoi(strings.TrimSpace(string(data)))
	if err != nil {
		return err
	}
	p, err := os.FindProcess(pid)
	if err != nil {
		return err
	}
	if err := p.Kill(); err != nil {
		return fmt.Errorf("gateway (pid %d) is not running: %w", pid, err)
	}
	_ = os.Remove(PidPath())
	return nil
}
