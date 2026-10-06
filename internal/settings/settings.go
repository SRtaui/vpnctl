// Package settings stores vpnctl's own configuration.
package settings

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"

	"github.com/SRtaui/vpnctl/internal/paths"
)

type Settings struct {
	// Subscription is the URL (or local file path) of the v2ray-style subscription.
	Subscription string `json:"subscription"`
	// AutoExclude is a regexp; matching servers stay selectable but are not used by "auto".
	AutoExclude string `json:"auto_exclude,omitempty"`
	// ExcludeProtocols drops whole protocols from the config, e.g. ["hysteria2"].
	ExcludeProtocols []string `json:"exclude_protocols,omitempty"`
	// ClashAPI is the local address of the control API.
	ClashAPI string `json:"clash_api"`
	// SingBoxPath is the sing-box binary to run.
	SingBoxPath string `json:"sing_box_path"`
	// DNS is the remote DNS-over-HTTPS server used through the proxy.
	DNS string `json:"dns"`
}

func Default() *Settings {
	return &Settings{
		ClashAPI:    "127.0.0.1:9090",
		SingBoxPath: paths.SingBoxBin,
		DNS:         "1.1.1.1",
	}
}

// Load reads the settings file, returning defaults if it does not exist.
func Load() (*Settings, error) {
	s := Default()
	data, err := os.ReadFile(paths.SettingsFile)
	if errors.Is(err, os.ErrNotExist) {
		return s, nil
	}
	if err != nil {
		return nil, err
	}
	if err := json.Unmarshal(data, s); err != nil {
		return nil, err
	}
	return s, nil
}

func (s *Settings) Save() error {
	data, err := json.MarshalIndent(s, "", "  ")
	if err != nil {
		return err
	}
	return WriteFileAtomic(paths.SettingsFile, append(data, '\n'), 0o600)
}

// WriteFileAtomic writes via a temp file + rename so readers never see a partial file.
func WriteFileAtomic(path string, data []byte, perm os.FileMode) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), ".tmp-*")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Chmod(perm); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmp.Name(), path)
}
