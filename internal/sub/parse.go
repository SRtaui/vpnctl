// Package sub fetches v2ray-style subscriptions and converts share links
// into sing-box outbound objects.
package sub

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"
)

// Outbound is a sing-box outbound object.
type Outbound = map[string]any

// Fetch downloads a subscription (or reads a local file) and returns its decoded share links.
func Fetch(src string) ([]string, error) {
	var raw []byte
	var err error
	if strings.HasPrefix(src, "http://") || strings.HasPrefix(src, "https://") {
		raw, err = download(src)
	} else {
		raw, err = os.ReadFile(src)
	}
	if err != nil {
		return nil, err
	}
	return Decode(string(raw))
}

func download(u string) ([]byte, error) {
	req, err := http.NewRequest(http.MethodGet, u, nil)
	if err != nil {
		return nil, err
	}
	// Panels return a plain base64 link list for v2rayN-like clients.
	req.Header.Set("User-Agent", "v2rayN/7.0")
	c := &http.Client{Timeout: 30 * time.Second}
	resp, err := c.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("subscription: HTTP %s", resp.Status)
	}
	return io.ReadAll(io.LimitReader(resp.Body, 16<<20))
}

// Decode turns subscription content (base64 or plain) into a list of share links.
func Decode(content string) ([]string, error) {
	content = strings.TrimSpace(content)
	if !strings.Contains(content, "://") {
		dec, err := b64(content)
		if err != nil {
			return nil, fmt.Errorf("subscription is neither a link list nor base64")
		}
		content = string(dec)
	}
	if strings.HasPrefix(content, "happ://") {
		return nil, fmt.Errorf("subscription is Happ-encrypted (happ://crypt); ask the provider for a regular link")
	}
	var links []string
	for _, l := range strings.Split(content, "\n") {
		l = strings.TrimSpace(l)
		if strings.Contains(l, "://") {
			links = append(links, l)
		}
	}
	if len(links) == 0 {
		return nil, fmt.Errorf("subscription contains no links")
	}
	return links, nil
}

func b64(s string) ([]byte, error) {
	s = strings.TrimSpace(s)
	s = strings.NewReplacer("\n", "", "\r", "", " ", "").Replace(s)
	for _, enc := range []*base64.Encoding{
		base64.StdEncoding, base64.RawStdEncoding, base64.URLEncoding, base64.RawURLEncoding,
	} {
		if d, err := enc.DecodeString(s); err == nil {
			return d, nil
		}
	}
	return nil, fmt.Errorf("invalid base64")
}

// Parse converts share links into outbounds. Tags are "NN name" and unique.
// Unsupported links are returned in skipped with a reason.
func Parse(links []string) (outs []Outbound, skipped []string) {
	for i, link := range links {
		name := linkName(link)
		if name == "" {
			name = fmt.Sprintf("srv%d", i+1)
		}
		tag := fmt.Sprintf("%02d %s", i+1, name)
		if r := []rune(tag); len(r) > 64 {
			tag = string(r[:64])
		}
		ob, err := parseLink(link, tag)
		if err != nil {
			skipped = append(skipped, fmt.Sprintf("%s: %v", tag, err))
			continue
		}
		outs = append(outs, ob)
	}
	return outs, skipped
}

func linkName(link string) string {
	if strings.HasPrefix(link, "vmess://") {
		if v, err := vmessJSON(link); err == nil {
			return strings.TrimSpace(str(v["ps"]))
		}
		return ""
	}
	if i := strings.LastIndex(link, "#"); i >= 0 {
		if n, err := url.PathUnescape(link[i+1:]); err == nil {
			return strings.TrimSpace(n)
		}
		return strings.TrimSpace(link[i+1:])
	}
	return ""
}

func parseLink(link, tag string) (Outbound, error) {
	scheme, _, _ := strings.Cut(link, "://")
	switch strings.ToLower(scheme) {
	case "vless":
		return parseVLESS(link, tag)
	case "trojan":
		return parseTrojan(link, tag)
	case "hysteria2", "hy2":
		return parseHy2(link, tag)
	case "ss":
		return parseSS(link, tag)
	case "vmess":
		return parseVMess(link, tag)
	default:
		return nil, fmt.Errorf("unsupported protocol %q", scheme)
	}
}

type params url.Values

func (p params) get(k, def string) string {
	if v := url.Values(p).Get(k); v != "" {
		return v
	}
	return def
}

func hostPort(u *url.URL, def int) (string, int, error) {
	host := u.Hostname()
	if host == "" {
		return "", 0, fmt.Errorf("missing host")
	}
	port := def
	if ps := u.Port(); ps != "" {
		p, err := strconv.Atoi(ps)
		if err != nil {
			return "", 0, fmt.Errorf("bad port %q", ps)
		}
		port = p
	}
	return host, port, nil
}

// transport builds a v2ray transport object; nil means plain TCP.
func transport(network, path, host, service string) (map[string]any, error) {
	switch network {
	case "", "tcp", "raw":
		return nil, nil
	case "ws":
		t := map[string]any{"type": "ws", "path": orDefault(path, "/")}
		if host != "" {
			t["headers"] = map[string]any{"Host": host}
		}
		return t, nil
	case "grpc":
		return map[string]any{"type": "grpc", "service_name": service}, nil
	case "http", "h2":
		t := map[string]any{"type": "http", "path": orDefault(path, "/")}
		if host != "" {
			t["host"] = strings.Split(host, ",")
		}
		return t, nil
	case "httpupgrade":
		t := map[string]any{"type": "httpupgrade", "path": orDefault(path, "/")}
		if host != "" {
			t["host"] = host
		}
		return t, nil
	default:
		// xhttp, splithttp, kcp, quic: not implemented in sing-box.
		return nil, fmt.Errorf("transport %q is not supported by sing-box", network)
	}
}

func tlsBlock(p params, security, defaultSNI string) map[string]any {
	if security != "tls" && security != "reality" {
		return nil
	}
	t := map[string]any{"enabled": true, "server_name": p.get("sni", p.get("peer", defaultSNI))}
	if fp := p.get("fp", ""); fp != "" {
		t["utls"] = map[string]any{"enabled": true, "fingerprint": fp}
	}
	if alpn := p.get("alpn", ""); alpn != "" {
		t["alpn"] = strings.Split(alpn, ",")
	}
	if p.get("allowInsecure", "") == "1" || p.get("insecure", "") == "1" {
		t["insecure"] = true
	}
	if security == "reality" {
		t["reality"] = map[string]any{
			"enabled":    true,
			"public_key": p.get("pbk", ""),
			"short_id":   p.get("sid", ""),
		}
		// Reality requires uTLS.
		if _, ok := t["utls"]; !ok {
			t["utls"] = map[string]any{"enabled": true, "fingerprint": "chrome"}
		}
	}
	return t
}

func parseVLESS(link, tag string) (Outbound, error) {
	u, err := url.Parse(link)
	if err != nil {
		return nil, err
	}
	host, port, err := hostPort(u, 443)
	if err != nil {
		return nil, err
	}
	p := params(u.Query())
	ob := Outbound{"type": "vless", "tag": tag, "server": host, "server_port": port, "uuid": u.User.Username()}
	if f := p.get("flow", ""); f != "" {
		ob["flow"] = f
	}
	if t := tlsBlock(p, p.get("security", "none"), host); t != nil {
		ob["tls"] = t
	}
	tr, err := transport(p.get("type", "tcp"), p.get("path", ""), p.get("host", ""), p.get("serviceName", ""))
	if err != nil {
		return nil, err
	}
	if tr != nil {
		ob["transport"] = tr
	}
	return ob, nil
}

func parseTrojan(link, tag string) (Outbound, error) {
	u, err := url.Parse(link)
	if err != nil {
		return nil, err
	}
	host, port, err := hostPort(u, 443)
	if err != nil {
		return nil, err
	}
	p := params(u.Query())
	ob := Outbound{"type": "trojan", "tag": tag, "server": host, "server_port": port, "password": u.User.Username()}
	if t := tlsBlock(p, p.get("security", "tls"), host); t != nil {
		ob["tls"] = t
	}
	tr, err := transport(p.get("type", "tcp"), p.get("path", ""), p.get("host", ""), p.get("serviceName", ""))
	if err != nil {
		return nil, err
	}
	if tr != nil {
		ob["transport"] = tr
	}
	return ob, nil
}

func parseHy2(link, tag string) (Outbound, error) {
	u, err := url.Parse(link)
	if err != nil {
		return nil, err
	}
	host, port, err := hostPort(u, 443)
	if err != nil {
		return nil, err
	}
	p := params(u.Query())
	password := u.User.Username()
	if pw, ok := u.User.Password(); ok {
		password += ":" + pw
	}
	t := map[string]any{"enabled": true, "server_name": p.get("sni", host)}
	if p.get("insecure", "") == "1" {
		t["insecure"] = true
	}
	ob := Outbound{"type": "hysteria2", "tag": tag, "server": host, "server_port": port, "password": password, "tls": t}
	if o := p.get("obfs", ""); o != "" && o != "none" {
		ob["obfs"] = map[string]any{"type": o, "password": p.get("obfs-password", "")}
	}
	return ob, nil
}

func parseSS(link, tag string) (Outbound, error) {
	body := strings.TrimPrefix(link, "ss://")
	if i := strings.Index(body, "#"); i >= 0 {
		body = body[:i]
	}
	// Legacy form: ss://base64(method:password@host:port)
	if !strings.Contains(body, "@") {
		dec, err := b64(strings.SplitN(body, "?", 2)[0])
		if err != nil {
			return nil, fmt.Errorf("bad ss link")
		}
		body = string(dec)
	}
	u, err := url.Parse("ss://" + body)
	if err != nil {
		return nil, err
	}
	host, port, err := hostPort(u, 8388)
	if err != nil {
		return nil, err
	}
	method, password := u.User.Username(), ""
	if pw, ok := u.User.Password(); ok {
		password = pw
	} else if dec, err := b64(method); err == nil { // SIP002: base64(method:password)
		method, password, _ = strings.Cut(string(dec), ":")
	}
	if method == "" || password == "" {
		return nil, fmt.Errorf("bad ss credentials")
	}
	if u.Query().Get("plugin") != "" {
		return nil, fmt.Errorf("ss plugins are not supported")
	}
	return Outbound{"type": "shadowsocks", "tag": tag, "server": host, "server_port": port,
		"method": method, "password": password}, nil
}

func vmessJSON(link string) (map[string]any, error) {
	dec, err := b64(strings.TrimPrefix(link, "vmess://"))
	if err != nil {
		return nil, err
	}
	var v map[string]any
	if err := json.Unmarshal(dec, &v); err != nil {
		return nil, err
	}
	return v, nil
}

func parseVMess(link, tag string) (Outbound, error) {
	v, err := vmessJSON(link)
	if err != nil {
		return nil, fmt.Errorf("bad vmess link")
	}
	port, err := strconv.Atoi(str(v["port"]))
	if err != nil {
		return nil, fmt.Errorf("bad port")
	}
	aid, _ := strconv.Atoi(str(v["aid"]))
	host := str(v["add"])
	ob := Outbound{"type": "vmess", "tag": tag, "server": host, "server_port": port,
		"uuid": str(v["id"]), "alter_id": aid, "security": orDefault(str(v["scy"]), "auto")}
	p := params(url.Values{
		"sni": {orDefault(str(v["sni"]), str(v["host"]))}, "fp": {str(v["fp"])}, "alpn": {str(v["alpn"])},
	})
	if t := tlsBlock(p, str(v["tls"]), host); t != nil {
		ob["tls"] = t
	}
	tr, err := transport(str(v["net"]), str(v["path"]), str(v["host"]), str(v["path"]))
	if err != nil {
		return nil, err
	}
	if tr != nil {
		ob["transport"] = tr
	}
	return ob, nil
}

func str(v any) string {
	switch x := v.(type) {
	case nil:
		return ""
	case string:
		return x
	case float64:
		return strconv.FormatFloat(x, 'f', -1, 64)
	default:
		return fmt.Sprint(x)
	}
}

func orDefault(s, d string) string {
	if s == "" {
		return d
	}
	return s
}
