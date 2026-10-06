// vpnctl — a small CLI that turns a v2ray-style VPN subscription into a
// sing-box TUN setup and lets you switch servers from the terminal.
package main

import (
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"syscall"
	"text/tabwriter"

	"vpnctl/internal/clash"
	"vpnctl/internal/install"
	"vpnctl/internal/paths"
	"vpnctl/internal/service"
	"vpnctl/internal/settings"
	"vpnctl/internal/singbox"
	"vpnctl/internal/sub"
)

var version = "dev"

const usage = `vpnctl — sing-box VPN manager

Usage:
  vpnctl setup <subscription-url> [--system] [--core-version X.Y.Z]
                                 install sing-box, rule-sets and the service, then start
  vpnctl on | off | restart      start / stop / restart the VPN
  vpnctl status                  show service state and current server
  vpnctl list                    list servers (* = selected)
  vpnctl use <N|name|auto>       switch server by number, exact name, or auto-select
  vpnctl ping                    measure latency of every server
  vpnctl update [--rules] [--core]
                                 re-fetch the subscription and rebuild the config;
                                 --rules also refreshes rule-sets, --core updates sing-box
  vpnctl set sub <url>           change subscription URL
  vpnctl set auto-exclude <re>   regexp of servers to keep out of auto-select ("" to clear)
  vpnctl set exclude-protocols <p1,p2>
                                 drop protocols entirely, e.g. hysteria2 ("" to clear)
  vpnctl set dns <ip>            remote DNS-over-HTTPS server (default 1.1.1.1)
  vpnctl logs [-f]               show sing-box logs
  vpnctl uninstall [--purge]     remove the service (--purge also deletes all data)
  vpnctl version

Web panel (when on): http://127.0.0.1:9090/ui
`

// Commands that touch system files or services.
var needRoot = map[string]bool{
	"setup": true, "on": true, "off": true, "restart": true, "update": true,
	"set": true, "logs": true, "uninstall": true,
}

func main() {
	if len(os.Args) < 2 {
		fmt.Print(usage)
		os.Exit(2)
	}
	cmd, args := os.Args[1], os.Args[2:]
	if needRoot[cmd] && os.Geteuid() != 0 {
		elevate()
	}
	if err := dispatch(cmd, args); err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
}

func dispatch(cmd string, args []string) error {
	switch cmd {
	case "setup":
		return cmdSetup(args)
	case "on", "start":
		return withService(func(m service.Manager) error { return m.Start() }, "VPN on")
	case "off", "stop":
		return withService(func(m service.Manager) error { return m.Stop() }, "VPN off")
	case "restart":
		return withService(func(m service.Manager) error { return m.Restart() }, "VPN restarted")
	case "status":
		return cmdStatus()
	case "list", "ls":
		return cmdList()
	case "use":
		return cmdUse(args)
	case "ping":
		return cmdPing()
	case "update":
		return cmdUpdate(args)
	case "set":
		return cmdSet(args)
	case "logs":
		fs := flag.NewFlagSet("logs", flag.ExitOnError)
		follow := fs.Bool("f", false, "follow")
		parse(fs, args)
		return service.Detect().Logs(*follow)
	case "uninstall":
		return cmdUninstall(args)
	case "version", "--version", "-v":
		fmt.Println("vpnctl", version)
		return nil
	case "help", "--help", "-h":
		fmt.Print(usage)
		return nil
	default:
		return fmt.Errorf("unknown command %q, see: vpnctl help", cmd)
	}
}

// elevate re-runs vpnctl through sudo or doas.
func elevate() {
	self, err := os.Executable()
	if err != nil {
		fmt.Fprintln(os.Stderr, "error: this command needs root")
		os.Exit(1)
	}
	for _, tool := range []string{"sudo", "doas"} {
		if p, err := exec.LookPath(tool); err == nil {
			err = syscall.Exec(p, append([]string{tool, self}, os.Args[1:]...), os.Environ())
			fmt.Fprintln(os.Stderr, "error:", err)
			os.Exit(1)
		}
	}
	fmt.Fprintln(os.Stderr, "error: this command needs root (sudo/doas not found)")
	os.Exit(1)
}

// parse lets flags appear anywhere among positional args.
func parse(fs *flag.FlagSet, args []string) []string {
	var flags, pos []string
	for i := 0; i < len(args); i++ {
		a := args[i]
		if a == "--" {
			pos = append(pos, args[i+1:]...)
			break
		}
		if strings.HasPrefix(a, "-") && len(a) > 1 {
			flags = append(flags, a)
			name := strings.TrimLeft(a, "-")
			if f := fs.Lookup(name); f != nil && !strings.Contains(a, "=") {
				if bf, ok := f.Value.(interface{ IsBoolFlag() bool }); !ok || !bf.IsBoolFlag() {
					if i+1 < len(args) {
						i++
						flags = append(flags, args[i])
					}
				}
			}
			continue
		}
		pos = append(pos, a)
	}
	_ = fs.Parse(flags)
	return pos
}

func withService(f func(service.Manager) error, msg string) error {
	if err := f(service.Detect()); err != nil {
		return err
	}
	fmt.Println(msg)
	return nil
}

// rebuild fetches the subscription, writes a validated sing-box config and
// returns the number of servers.
func rebuild(s *settings.Settings) error {
	if s.Subscription == "" {
		return errors.New("no subscription configured, run: vpnctl set sub <url>")
	}
	if !install.RuleSetsPresent() {
		fmt.Println("downloading rule-sets…")
		if err := install.RuleSets(); err != nil {
			return err
		}
	}
	fmt.Println("fetching subscription…")
	links, err := sub.Fetch(s.Subscription)
	if err != nil {
		return err
	}
	outs, skipped := sub.Parse(links)
	for _, sk := range skipped {
		fmt.Println("  skip:", sk)
	}
	cfg, err := singbox.Build(singbox.Options{
		Outbounds:        outs,
		AutoExclude:      s.AutoExclude,
		ExcludeProtocols: s.ExcludeProtocols,
		ClashAPI:         s.ClashAPI,
		DNS:              s.DNS,
	})
	if err != nil {
		return err
	}
	data, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return err
	}

	// Validate with sing-box before replacing the working config.
	tmp := paths.SingBoxConfig + ".new"
	if err := settings.WriteFileAtomic(tmp, data, 0o600); err != nil {
		return err
	}
	defer os.Remove(tmp)
	if out, err := exec.Command(s.SingBoxPath, "check", "-c", tmp).CombinedOutput(); err != nil {
		return fmt.Errorf("sing-box rejected the config:\n%s", out)
	}
	if err := os.Rename(tmp, paths.SingBoxConfig); err != nil {
		return err
	}
	fmt.Printf("config: %d servers (%d skipped)\n", len(cfg["outbounds"].([]any))-3, len(skipped))
	return nil
}

func cmdSetup(args []string) error {
	fs := flag.NewFlagSet("setup", flag.ExitOnError)
	useSystem := fs.Bool("system", false, "use sing-box from PATH instead of downloading it")
	coreVer := fs.String("core-version", "", "sing-box version to download (default: latest)")
	pos := parse(fs, args)

	s, err := settings.Load()
	if err != nil {
		return err
	}
	if len(pos) > 0 {
		s.Subscription = pos[0]
	}
	if s.Subscription == "" {
		return errors.New("usage: vpnctl setup <subscription-url>")
	}
	for _, d := range []string{paths.EtcDir, paths.LibDir} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			return err
		}
	}

	if *useSystem {
		p, err := install.SystemSingBox()
		if err != nil {
			return errors.New("sing-box not found in PATH")
		}
		s.SingBoxPath = p
	} else {
		fmt.Println("downloading sing-box…")
		v, err := install.SingBox(*coreVer)
		if err != nil {
			return err
		}
		fmt.Println("  sing-box", v)
		s.SingBoxPath = paths.SingBoxBin
	}
	if v, err := install.CheckVersion(s.SingBoxPath); err != nil {
		return err
	} else if *useSystem {
		fmt.Println("using", s.SingBoxPath, v)
	}

	fmt.Println("downloading rule-sets…")
	if err := install.RuleSets(); err != nil {
		return err
	}
	if err := rebuild(s); err != nil {
		return err
	}
	if err := s.Save(); err != nil {
		return err
	}

	m := service.Detect()
	if err := m.Install(s.SingBoxPath); err != nil {
		return fmt.Errorf("install %s service: %w", m.Name(), err)
	}
	for _, u := range service.ConflictingUnits() {
		fmt.Printf("warning: %s is running and will conflict; disable it: sudo systemctl disable --now %s\n", u, u)
	}
	if err := m.Restart(); err != nil {
		return err
	}
	fmt.Printf("done: VPN on (service: %s)\n", m.Name())
	fmt.Println("try: vpnctl list | vpnctl use <N> | vpnctl off")
	return nil
}

func cmdUpdate(args []string) error {
	fs := flag.NewFlagSet("update", flag.ExitOnError)
	rules := fs.Bool("rules", false, "re-download rule-sets")
	core := fs.Bool("core", false, "update the downloaded sing-box to the latest version")
	parse(fs, args)

	s, err := settings.Load()
	if err != nil {
		return err
	}
	if *core {
		if s.SingBoxPath != paths.SingBoxBin {
			fmt.Println("sing-box comes from your system (" + s.SingBoxPath + "), update it with your package manager")
		} else {
			v, err := install.SingBox("")
			if err != nil {
				return err
			}
			fmt.Println("sing-box", v)
		}
	}
	if *rules {
		fmt.Println("downloading rule-sets…")
		if err := install.RuleSets(); err != nil {
			return err
		}
	}
	if err := rebuild(s); err != nil {
		return err
	}
	if m := service.Detect(); m.Active() {
		return m.Restart()
	}
	return nil
}

func cmdSet(args []string) error {
	if len(args) < 1 {
		return errors.New("usage: vpnctl set <sub|auto-exclude|exclude-protocols|dns> <value>")
	}
	val := ""
	if len(args) > 1 {
		val = args[1]
	}
	s, err := settings.Load()
	if err != nil {
		return err
	}
	switch args[0] {
	case "sub", "subscription":
		if val == "" {
			return errors.New("usage: vpnctl set sub <url>")
		}
		s.Subscription = val
	case "auto-exclude":
		s.AutoExclude = val
	case "exclude-protocols":
		s.ExcludeProtocols = nil
		for _, p := range strings.Split(val, ",") {
			if p = strings.TrimSpace(p); p != "" {
				s.ExcludeProtocols = append(s.ExcludeProtocols, p)
			}
		}
	case "dns":
		if val == "" {
			return errors.New("usage: vpnctl set dns <ip>")
		}
		s.DNS = val
	default:
		return fmt.Errorf("unknown setting %q", args[0])
	}
	if err := rebuild(s); err != nil {
		return err
	}
	if err := s.Save(); err != nil {
		return err
	}
	if m := service.Detect(); m.Active() {
		return m.Restart()
	}
	return nil
}

func api() *clash.Client {
	s, err := settings.Load()
	if err != nil || s.ClashAPI == "" {
		return clash.New(settings.Default().ClashAPI)
	}
	return clash.New(s.ClashAPI)
}

func cmdStatus() error {
	m := service.Detect()
	state := "off"
	if m.Active() {
		state = "on"
	}
	fmt.Printf("VPN:      %s\nservice:  %s\n", state, m.Name())
	if s, err := settings.Load(); err == nil {
		if _, v, err := install.Version(s.SingBoxPath); err == nil {
			fmt.Printf("sing-box: %s (%s)\n", v, s.SingBoxPath)
		}
	}
	if state == "on" {
		c := api()
		if g, err := c.Group(clash.Group); err == nil {
			now := g.Now
			if now == "auto" {
				if a, err := c.Group("auto"); err == nil && a.Now != "" {
					now = "auto → " + a.Now
				}
			}
			fmt.Printf("server:   %s\n", now)
		}
	}
	return nil
}

func cmdList() error {
	g, err := api().Group(clash.Group)
	if err != nil {
		return err
	}
	for _, n := range g.All {
		mark := "  "
		if n == g.Now {
			mark = "* "
		}
		fmt.Println(mark + n)
	}
	return nil
}

// resolve matches a number ("7", "07"), an exact name, or "auto".
func resolve(all []string, q string) (string, error) {
	for _, n := range all {
		if n == q {
			return n, nil
		}
	}
	num := q
	if len(num) == 1 {
		num = "0" + num
	}
	for _, n := range all {
		if first, _, _ := strings.Cut(n, " "); first == num {
			return n, nil
		}
	}
	// Unique case-insensitive substring as a convenience.
	var hits []string
	for _, n := range all {
		if strings.Contains(strings.ToLower(n), strings.ToLower(q)) {
			hits = append(hits, n)
		}
	}
	if len(hits) == 1 {
		return hits[0], nil
	}
	if len(hits) > 1 {
		return "", fmt.Errorf("%q matches %d servers, be more specific", q, len(hits))
	}
	return "", fmt.Errorf("server %q not found, see: vpnctl list", q)
}

func cmdUse(args []string) error {
	if len(args) != 1 {
		return errors.New("usage: vpnctl use <N|name|auto>")
	}
	c := api()
	g, err := c.Group(clash.Group)
	if err != nil {
		return err
	}
	name, err := resolve(g.All, args[0])
	if err != nil {
		return err
	}
	if err := c.Select(clash.Group, name); err != nil {
		return err
	}
	fmt.Println("→", name)
	return nil
}

func cmdPing() error {
	c := api()
	g, err := c.Group(clash.Group)
	if err != nil {
		return err
	}
	var names []string
	for _, n := range g.All {
		if n != "auto" {
			names = append(names, n)
		}
	}
	w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', tabwriter.AlignRight)
	for _, r := range c.DelayAll(names) {
		d := "timeout"
		if r.Delay >= 0 {
			d = fmt.Sprintf("%d ms", r.Delay)
		}
		fmt.Fprintf(w, "%s\t  %s\t\n", d, r.Name)
	}
	return w.Flush()
}

func cmdUninstall(args []string) error {
	fs := flag.NewFlagSet("uninstall", flag.ExitOnError)
	purge := fs.Bool("purge", false, "also delete settings, downloaded sing-box and rule-sets")
	parse(fs, args)
	if err := service.Detect().Uninstall(); err != nil {
		return err
	}
	if *purge {
		for _, p := range []string{paths.EtcDir, paths.LibDir, paths.LogFile} {
			if err := os.RemoveAll(p); err != nil {
				return err
			}
		}
	}
	fmt.Println("vpnctl service removed")
	return nil
}
