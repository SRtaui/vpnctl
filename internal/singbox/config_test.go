package singbox

import (
	"encoding/json"
	"os"
	"os/exec"
	"testing"

	"github.com/SRtaui/vpnctl/internal/sub"
)

func TestBuildAutoExclude(t *testing.T) {
	outs, _ := sub.Parse([]string{
		"vless://11111111-2222-3333-4444-555555555555@1.2.3.4:443?security=reality&pbk=K&sid=1&sni=a.com#DE",
		"vless://11111111-2222-3333-4444-555555555555@5.6.7.8:443?security=reality&pbk=K&sid=1&sni=a.com#🇷🇺 RU-1",
		"hysteria2://pw@h.com:443#HY",
	})
	cfg, err := Build(Options{Outbounds: outs, AutoExclude: "RU", ExcludeProtocols: []string{"hysteria2"},
		ClashAPI: "127.0.0.1:9090", DNS: "1.1.1.1"})
	if err != nil {
		t.Fatal(err)
	}
	obs := cfg["outbounds"].([]any)
	sel := obs[0].(map[string]any)["outbounds"].([]string)
	auto := obs[1].(map[string]any)["outbounds"].([]string)
	if len(sel) != 3 || len(auto) != 1 || auto[0] != "01 DE" {
		t.Fatalf("selector=%v auto=%v", sel, auto)
	}

	// Optional: validate with a real sing-box binary if SINGBOX_BIN is set.
	bin := os.Getenv("SINGBOX_BIN")
	if bin == "" {
		t.Skip("SINGBOX_BIN not set")
	}
	f, _ := os.CreateTemp("", "cfg-*.json")
	defer os.Remove(f.Name())
	json.NewEncoder(f).Encode(cfg)
	f.Close()
	if out, err := exec.Command(bin, "check", "-c", f.Name()).CombinedOutput(); err != nil {
		t.Fatalf("sing-box check: %v\n%s", err, out)
	}
}
