// Package bifrost installs, configures and runs the Bifrost gateway that every model call goes through, so a fresh
// machine needs nothing but the slendertoken binary and an API key from any provider.
package bifrost

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/Ani07-05/slendertoken/internal/config"
)

// Endpoints are variables so tests can point them at a local server.
var (
	LatestURL    = "https://getbifrost.ai/latest-release"
	DownloadBase = "https://downloads.getmaxim.ai"
)

// Home is where the binary, data and logs live.
func Home() string { return filepath.Join(config.Home(), ".slendertoken", "bifrost") }

func DataDir() string { return filepath.Join(Home(), "data") }

func BinaryPath() string {
	name := "bifrost-http"
	if runtime.GOOS == "windows" {
		name += ".exe"
	}
	return filepath.Join(Home(), "bin", name)
}

// platform is the os and arch names Bifrost's download server uses.
func platform() (goos, arch string, err error) {
	switch runtime.GOOS {
	case "darwin", "linux", "windows":
		goos = runtime.GOOS
	default:
		return "", "", fmt.Errorf("no Bifrost build for %s", runtime.GOOS)
	}
	switch runtime.GOARCH {
	case "amd64", "arm64":
		arch = runtime.GOARCH
	default:
		return "", "", fmt.Errorf("no Bifrost build for %s", runtime.GOARCH)
	}
	return goos, arch, nil
}

func LatestVersion(ctx context.Context) (string, error) {
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, LatestURL, nil)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("cannot look up the latest Bifrost release: %w", err)
	}
	defer resp.Body.Close()
	var v struct{ Name string }
	if resp.StatusCode != 200 || json.NewDecoder(resp.Body).Decode(&v) != nil || v.Name == "" {
		return "", fmt.Errorf("cannot look up the latest Bifrost release (HTTP %d)", resp.StatusCode)
	}
	return v.Name, nil
}

type progress struct {
	r     io.Reader
	total int64
	read  int64
	next  int64
	log   func(string)
}

func (p *progress) Read(b []byte) (int, error) {
	n, err := p.r.Read(b)
	p.read += int64(n)
	if p.total > 0 && p.read*10/p.total >= p.next {
		p.log(fmt.Sprintf("downloading Bifrost... %d%%", p.next*10))
		p.next++
	}
	return n, err
}

// Install downloads the Bifrost binary unless it is already there, and returns its path.
func Install(ctx context.Context, log func(string)) (string, error) {
	bin := BinaryPath()
	if st, err := os.Stat(bin); err == nil && st.Size() > 0 {
		return bin, nil
	}
	goos, arch, err := platform()
	if err != nil {
		return "", err
	}
	version, err := LatestVersion(ctx)
	if err != nil {
		return "", err
	}
	url := fmt.Sprintf("%s/bifrost/%s/%s/%s/%s", DownloadBase, version, goos, arch, filepath.Base(bin))
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("cannot download Bifrost: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return "", fmt.Errorf("cannot download Bifrost %s for %s/%s (HTTP %d)", version, goos, arch, resp.StatusCode)
	}
	if err := os.MkdirAll(filepath.Dir(bin), 0o755); err != nil {
		return "", err
	}
	tmp := bin + ".part"
	out, err := os.OpenFile(tmp, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o755)
	if err != nil {
		return "", err
	}
	log(fmt.Sprintf("downloading Bifrost %s...", version))
	_, err = io.Copy(out, &progress{r: resp.Body, total: resp.ContentLength, log: log})
	if cerr := out.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		_ = os.Remove(tmp)
		return "", fmt.Errorf("download interrupted: %w", err)
	}
	if err := os.Rename(tmp, bin); err != nil {
		return "", err
	}
	_ = os.WriteFile(filepath.Join(Home(), "VERSION"), []byte(strings.TrimSpace(version)+"\n"), 0o644)
	return bin, nil
}

var errNotManaged = errors.New("Bifrost is not set up by slendertoken yet: run `slendertoken setup`")

// Managed reports whether `slendertoken setup` has written a gateway config on this machine.
func Managed() bool {
	_, err := os.Stat(ConfigPath())
	return err == nil
}

func waitHealthy(ctx context.Context, url string, limit time.Duration) error {
	deadline := time.Now().Add(limit)
	for time.Now().Before(deadline) {
		if Healthy(url) {
			return nil
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(500 * time.Millisecond):
		}
	}
	return fmt.Errorf("Bifrost did not come up within %s; see %s", limit, LogPath())
}

// Healthy reports whether a gateway answers its health check at url.
func Healthy(url string) bool {
	c := http.Client{Timeout: 2 * time.Second}
	resp, err := c.Get(strings.TrimRight(url, "/") + "/health")
	if err != nil {
		return false
	}
	resp.Body.Close()
	return resp.StatusCode == 200
}
