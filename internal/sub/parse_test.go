package sub

import (
	"encoding/base64"
	"strings"
	"testing"
)

const uuid = "11111111-2222-3333-4444-555555555555"

func TestDecodeBase64(t *testing.T) {
	list := "vless://" + uuid + "@1.2.3.4:443?security=none#A\nhysteria2://pw@h.example:443#B\n"
	links, err := Decode(base64.StdEncoding.EncodeToString([]byte(list)))
	if err != nil {
		t.Fatal(err)
	}
	if len(links) != 2 {
		t.Fatalf("want 2 links, got %d", len(links))
	}
}

func TestDecodeHapp(t *testing.T) {
	if _, err := Decode("happ://crypt3/abc"); err == nil || !strings.Contains(err.Error(), "Happ") {
		t.Fatalf("expected Happ error, got %v", err)
	}
}

func TestVLESSReality(t *testing.T) {
	link := "vless://" + uuid + "@1.2.3.4:444?encryption=none&flow=xtls-rprx-vision&type=tcp&security=reality&sni=a.com&fp=firefox&pbk=KEY&sid=54#%F0%9F%87%A9%F0%9F%87%AA%20DE"
	outs, skipped := Parse([]string{link})
	if len(skipped) != 0 || len(outs) != 1 {
		t.Fatalf("outs=%v skipped=%v", outs, skipped)
	}
	o := outs[0]
	if o["tag"] != "01 🇩🇪 DE" || o["server_port"] != 444 || o["flow"] != "xtls-rprx-vision" {
		t.Fatalf("bad outbound: %v", o)
	}
	tls := o["tls"].(map[string]any)
	r := tls["reality"].(map[string]any)
	if r["public_key"] != "KEY" || r["short_id"] != "54" || tls["server_name"] != "a.com" {
		t.Fatalf("bad tls: %v", tls)
	}
	if tls["utls"].(map[string]any)["fingerprint"] != "firefox" {
		t.Fatalf("bad fingerprint: %v", tls)
	}
}

func TestVLESSWS(t *testing.T) {
	link := "vless://" + uuid + "@h.com:443?type=ws&path=%2Fws&host=cdn.com&security=tls&sni=cdn.com#WS"
	outs, _ := Parse([]string{link})
	tr := outs[0]["transport"].(map[string]any)
	if tr["type"] != "ws" || tr["path"] != "/ws" || tr["headers"].(map[string]any)["Host"] != "cdn.com" {
		t.Fatalf("bad transport: %v", tr)
	}
}

func TestXHTTPSkipped(t *testing.T) {
	_, skipped := Parse([]string{"vless://" + uuid + "@h.com:445?type=xhttp&security=reality&pbk=K#X"})
	if len(skipped) != 1 || !strings.Contains(skipped[0], "xhttp") {
		t.Fatalf("expected xhttp skip, got %v", skipped)
	}
}

func TestHysteria2(t *testing.T) {
	outs, _ := Parse([]string{"hysteria2://" + uuid + "@hy.com:443/?sni=hy.com&obfs=salamander&obfs-password=p#HY"})
	o := outs[0]
	if o["type"] != "hysteria2" || o["password"] != uuid || o["obfs"].(map[string]any)["password"] != "p" {
		t.Fatalf("bad hy2: %v", o)
	}
}

func TestShadowsocks(t *testing.T) {
	cred := base64.RawURLEncoding.EncodeToString([]byte("chacha20-ietf-poly1305:secret"))
	sip002 := "ss://" + cred + "@1.2.3.4:8388#SS"
	legacy := "ss://" + base64.StdEncoding.EncodeToString([]byte("aes-256-gcm:pw@5.6.7.8:443")) + "#OLD"
	outs, skipped := Parse([]string{sip002, legacy})
	if len(skipped) != 0 {
		t.Fatal(skipped)
	}
	if outs[0]["method"] != "chacha20-ietf-poly1305" || outs[0]["password"] != "secret" {
		t.Fatalf("bad sip002: %v", outs[0])
	}
	if outs[1]["server"] != "5.6.7.8" || outs[1]["method"] != "aes-256-gcm" || outs[1]["password"] != "pw" {
		t.Fatalf("bad legacy: %v", outs[1])
	}
}

func TestVMess(t *testing.T) {
	js := `{"v":"2","ps":"VM","add":"v.com","port":"443","id":"` + uuid + `","aid":"0","net":"ws","path":"/p","host":"v.com","tls":"tls"}`
	outs, skipped := Parse([]string{"vmess://" + base64.StdEncoding.EncodeToString([]byte(js))})
	if len(skipped) != 0 {
		t.Fatal(skipped)
	}
	o := outs[0]
	if o["tag"] != "01 VM" || o["server_port"] != 443 || o["transport"].(map[string]any)["path"] != "/p" {
		t.Fatalf("bad vmess: %v", o)
	}
}

func TestTrojan(t *testing.T) {
	outs, _ := Parse([]string{"trojan://pass@t.com:443?sni=t.com#TR"})
	o := outs[0]
	if o["password"] != "pass" || o["tls"].(map[string]any)["server_name"] != "t.com" {
		t.Fatalf("bad trojan: %v", o)
	}
}
