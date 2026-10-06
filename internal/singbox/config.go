// Package singbox builds sing-box (1.12+) configuration files.
package singbox

import (
	"fmt"
	"regexp"
	"slices"

	"github.com/SRtaui/vpnctl/internal/paths"
	"github.com/SRtaui/vpnctl/internal/sub"
)

type Options struct {
	Outbounds        []sub.Outbound
	AutoExclude      string   // regexp; matching tags are left out of "auto"
	ExcludeProtocols []string // outbound types to drop entirely
	ClashAPI         string
	DNS              string
}

// Build returns the full sing-box config.
func Build(o Options) (map[string]any, error) {
	var re *regexp.Regexp
	if o.AutoExclude != "" {
		var err error
		if re, err = regexp.Compile(o.AutoExclude); err != nil {
			return nil, fmt.Errorf("auto_exclude: %w", err)
		}
	}

	var obs []any
	var all, auto []string
	for _, ob := range o.Outbounds {
		if slices.Contains(o.ExcludeProtocols, ob["type"].(string)) {
			continue
		}
		tag := ob["tag"].(string)
		obs = append(obs, ob)
		all = append(all, tag)
		if re == nil || !re.MatchString(tag) {
			auto = append(auto, tag)
		}
	}
	if len(all) == 0 {
		return nil, fmt.Errorf("no usable servers")
	}
	if len(auto) == 0 {
		auto = all
	}

	outbounds := []any{
		map[string]any{"type": "selector", "tag": "proxy", "outbounds": append([]string{"auto"}, all...), "default": "auto"},
		map[string]any{"type": "urltest", "tag": "auto", "outbounds": auto,
			"url": "https://www.gstatic.com/generate_204", "interval": "10m", "tolerance": 100},
	}
	outbounds = append(outbounds, obs...)
	outbounds = append(outbounds, map[string]any{"type": "direct", "tag": "direct"})

	return map[string]any{
		"log": map[string]any{"level": "warn", "timestamp": true},
		"dns": map[string]any{
			"servers": []any{
				map[string]any{"tag": "remote", "type": "https", "server": o.DNS, "detour": "proxy"},
				map[string]any{"tag": "local", "type": "local"},
			},
			"rules": []any{map[string]any{"rule_set": "geosite-ru", "server": "local"}},
			"final": "remote",
		},
		"inbounds": []any{map[string]any{
			"type": "tun", "tag": "tun-in", "interface_name": "vpnctl0",
			"address":    []string{"172.19.0.1/30"},
			"auto_route": true, "strict_route": true, "stack": "system",
		}},
		"outbounds": outbounds,
		"route": map[string]any{
			"rules": []any{
				map[string]any{"action": "sniff"},
				map[string]any{"protocol": "dns", "action": "hijack-dns"},
				map[string]any{"ip_is_private": true, "outbound": "direct"},
				map[string]any{"rule_set": []string{"geosite-ru", "geoip-ru"}, "outbound": "direct"},
			},
			"rule_set": []any{
				map[string]any{"tag": "geosite-ru", "type": "local", "format": "binary", "path": paths.GeositeRU},
				map[string]any{"tag": "geoip-ru", "type": "local", "format": "binary", "path": paths.GeoipRU},
			},
			"final":                   "proxy",
			"auto_detect_interface":   true,
			"default_domain_resolver": "local",
		},
		"experimental": map[string]any{
			"clash_api": map[string]any{
				"external_controller":      o.ClashAPI,
				"external_ui":              "ui",
				"external_ui_download_url": "https://github.com/MetaCubeX/metacubexd/archive/refs/heads/gh-pages.zip",
			},
			"cache_file": map[string]any{"enabled": true},
		},
	}, nil
}
