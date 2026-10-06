# vpnctl

A small, dependency-free CLI that turns a v2ray-style VPN subscription into a
[sing-box](https://github.com/SagerNet/sing-box) TUN setup and lets you switch
servers from the terminal. Works on any Linux distribution.

- **Lightweight**: one static binary plus sing-box (~20–40 MB RAM), no GUI, no Python.
- **Any distro**: downloads sing-box itself; runs as a systemd or OpenRC service,
  or as a plain background process on systems without either.
- **Subscription formats**: `vless://` (Reality / TLS; tcp, ws, grpc, http, httpupgrade),
  `hysteria2://`, `trojan://`, `ss://`, `vmess://`. Base64 or plain link lists.
- **Auto-select**: picks the fastest server every 10 minutes and fails over automatically.
- **Split routing**: Russian sites and LAN go direct; everything else through the VPN.
- **Web panel**: [metacubexd](https://github.com/MetaCubeX/metacubexd) at `http://127.0.0.1:9090/ui`.

## Install

Download the binary for your architecture from
[Releases](../../releases/latest) and put it in your `PATH`:

```sh
curl -fLo vpnctl https://github.com/SRtaui/vpnctl/releases/latest/download/vpnctl-linux-amd64
sudo install -m755 vpnctl /usr/local/bin/vpnctl
```

Or with Go 1.22+:

```sh
go install github.com/SRtaui/vpnctl@latest   # installs to ~/go/bin
```

Or build from source:

```sh
git clone https://github.com/SRtaui/vpnctl && cd vpnctl
make && sudo make install
```

## Quick start

```sh
vpnctl setup 'https://your-provider/sub/xxxx'
```

This downloads sing-box and rule-sets, builds the config, installs the
`vpnctl` service (autostart on boot) and turns the VPN on.
Commands that need root re-run themselves through `sudo`/`doas`.

Already have sing-box from your package manager (1.12+)? Use it instead:

```sh
vpnctl setup 'https://your-provider/sub/xxxx' --system
```

## Usage

```text
vpnctl on | off | restart      start / stop / restart the VPN
vpnctl status                  service state and current server
vpnctl list                    list servers (* = selected)
vpnctl use 13                  switch by number
vpnctl use fnk                 …or by a unique part of the name
vpnctl use auto                back to automatic selection
vpnctl ping                    latency of every server, fastest first
vpnctl update                  re-fetch the subscription
vpnctl update --rules --core   also refresh rule-sets and sing-box
vpnctl logs [-f]               sing-box logs
vpnctl uninstall [--purge]     remove the service (--purge: all data)
```

### Settings

```sh
vpnctl set sub 'https://new-url'          # change subscription
vpnctl set auto-exclude 'RU|🇷🇺'           # keep these out of auto-select
vpnctl set exclude-protocols hysteria2    # drop a protocol (saves battery)
vpnctl set dns 8.8.8.8                    # remote DoH server
```

Pass an empty string (`''`) to clear `auto-exclude` / `exclude-protocols`.

## Files

| Path | Purpose |
|---|---|
| `/etc/vpnctl/settings.json` | vpnctl settings (subscription URL etc.) |
| `/etc/vpnctl/sing-box.json` | generated sing-box config |
| `/var/lib/vpnctl/bin/sing-box` | downloaded sing-box |
| `/var/lib/vpnctl/rules/*.srs` | geosite/geoip rule-sets |
| `/var/lib/vpnctl/cache.db` | remembers the selected server |

Every new config is validated with `sing-box check` before it replaces the
working one, so a broken subscription never breaks a running VPN.

## Notes

- Transports sing-box does not implement (`xhttp`, `splithttp`, `kcp`) are
  skipped with a message during `setup`/`update`.
- Happ-encrypted subscriptions (`happ://crypt…`) can't be read; ask your
  provider for a regular link.
- Don't run another TUN VPN (Happ, a distro `sing-box.service`, …) at the same time.

## License

MIT
