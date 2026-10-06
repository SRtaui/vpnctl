// Package install downloads the sing-box core and rule-set files.
package install

import (
	"archive/tar"
	"compress/gzip"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"regexp"
	"runtime"
	"strconv"
	"strings"
	"time"

	"github.com/SRtaui/vpnctl/internal/paths"
	"github.com/SRtaui/vpnctl/internal/settings"
)

// MinVersion is the oldest sing-box the generated config works with.
var MinVersion = [3]int{1, 12, 0}

var client = &http.Client{Timeout: 5 * time.Minute}

func get(u string) (*http.Response, error) {
	req, err := http.NewRequest(http.MethodGet, u, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", "vpnctl")
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode != http.StatusOK {
		resp.Body.Close()
		return nil, fmt.Errorf("GET %s: %s", u, resp.Status)
	}
	return resp, nil
}

// LatestVersion asks GitHub for the newest stable sing-box release ("1.12.3").
func LatestVersion() (string, error) {
	resp, err := get("https://api.github.com/repos/SagerNet/sing-box/releases/latest")
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	var rel struct {
		TagName string `json:"tag_name"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&rel); err != nil {
		return "", err
	}
	return strings.TrimPrefix(rel.TagName, "v"), nil
}

func goarchToAsset() (string, error) {
	switch runtime.GOARCH {
	case "amd64", "arm64", "386", "s390x", "riscv64", "loong64":
		return runtime.GOARCH, nil
	case "arm":
		return "armv7", nil
	default:
		return "", fmt.Errorf("unsupported architecture %s", runtime.GOARCH)
	}
}

// SingBox downloads sing-box (version "" = latest) into paths.SingBoxBin.
func SingBox(version string) (string, error) {
	if version == "" {
		v, err := LatestVersion()
		if err != nil {
			return "", fmt.Errorf("get latest sing-box version: %w", err)
		}
		version = v
	}
	version = strings.TrimPrefix(version, "v")
	arch, err := goarchToAsset()
	if err != nil {
		return "", err
	}
	name := fmt.Sprintf("sing-box-%s-linux-%s", version, arch)
	u := fmt.Sprintf("https://github.com/SagerNet/sing-box/releases/download/v%s/%s.tar.gz", version, name)

	resp, err := get(u)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	gz, err := gzip.NewReader(resp.Body)
	if err != nil {
		return "", err
	}
	tr := tar.NewReader(gz)
	for {
		h, err := tr.Next()
		if err == io.EOF {
			return "", fmt.Errorf("sing-box binary not found in %s", u)
		}
		if err != nil {
			return "", err
		}
		if h.Typeflag != tar.TypeReg || path.Base(h.Name) != "sing-box" {
			continue
		}
		if err := os.MkdirAll(paths.BinDir, 0o755); err != nil {
			return "", err
		}
		tmp := paths.SingBoxBin + ".new"
		f, err := os.OpenFile(tmp, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o755)
		if err != nil {
			return "", err
		}
		if _, err := io.Copy(f, tr); err != nil {
			f.Close()
			os.Remove(tmp)
			return "", err
		}
		f.Close()
		// Rename over the old binary is safe even while it is running.
		return version, os.Rename(tmp, paths.SingBoxBin)
	}
}

var verRe = regexp.MustCompile(`version (\d+)\.(\d+)\.(\d+)`)

// Version returns the version reported by a sing-box binary.
func Version(bin string) ([3]int, string, error) {
	out, err := exec.Command(bin, "version").Output()
	if err != nil {
		return [3]int{}, "", err
	}
	m := verRe.FindStringSubmatch(string(out))
	if m == nil {
		return [3]int{}, "", fmt.Errorf("cannot parse sing-box version")
	}
	var v [3]int
	for i := range v {
		v[i], _ = strconv.Atoi(m[i+1])
	}
	return v, fmt.Sprintf("%d.%d.%d", v[0], v[1], v[2]), nil
}

// CheckVersion fails if bin is older than MinVersion.
func CheckVersion(bin string) (string, error) {
	v, s, err := Version(bin)
	if err != nil {
		return "", fmt.Errorf("%s: %w", bin, err)
	}
	for i := range v {
		if v[i] != MinVersion[i] {
			if v[i] < MinVersion[i] {
				return s, fmt.Errorf("sing-box %s is too old, need %d.%d+", s, MinVersion[0], MinVersion[1])
			}
			break
		}
	}
	return s, nil
}

var ruleSets = []struct{ dst, repo, file string }{
	{paths.GeositeRU, "sing-geosite", "geosite-category-ru.srs"},
	{paths.GeoipRU, "sing-geoip", "geoip-ru.srs"},
}

// RuleSetsPresent reports whether all rule-set files exist.
func RuleSetsPresent() bool {
	for _, r := range ruleSets {
		if _, err := os.Stat(r.dst); err != nil {
			return false
		}
	}
	return true
}

// RuleSets downloads the RU geosite/geoip rule-sets, trying mirrors in order.
func RuleSets() error {
	if err := os.MkdirAll(paths.RuleSetDir, 0o755); err != nil {
		return err
	}
	for _, r := range ruleSets {
		mirrors := []string{
			fmt.Sprintf("https://cdn.jsdelivr.net/gh/SagerNet/%s@rule-set/%s", r.repo, r.file),
			fmt.Sprintf("https://raw.githubusercontent.com/SagerNet/%s/rule-set/%s", r.repo, r.file),
			fmt.Sprintf("https://testingcf.jsdelivr.net/gh/SagerNet/%s@rule-set/%s", r.repo, r.file),
		}
		var lastErr error
		for _, u := range mirrors {
			if lastErr = fetchTo(u, r.dst); lastErr == nil {
				break
			}
		}
		if lastErr != nil {
			return fmt.Errorf("download %s: %w", r.file, lastErr)
		}
	}
	return nil
}

func fetchTo(u, dst string) error {
	resp, err := get(u)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(io.LimitReader(resp.Body, 64<<20))
	if err != nil {
		return err
	}
	if len(data) == 0 {
		return fmt.Errorf("empty file")
	}
	return settings.WriteFileAtomic(dst, data, 0o644)
}

// SystemSingBox returns sing-box from PATH, if any.
func SystemSingBox() (string, error) {
	p, err := exec.LookPath("sing-box")
	if err != nil {
		return "", err
	}
	return filepath.Abs(p)
}
