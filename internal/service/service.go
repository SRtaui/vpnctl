// Package service runs sing-box in the background via systemd, OpenRC,
// or a plain detached process with a pid file.
package service

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/SRtaui/vpnctl/internal/paths"
	"github.com/SRtaui/vpnctl/internal/settings"
)

type Manager interface {
	Name() string
	// Install registers the service (and enables autostart where supported).
	Install(bin string) error
	Uninstall() error
	Start() error
	Stop() error
	Restart() error
	Active() bool
	// Logs shows recent log output (follow=true streams it).
	Logs(follow bool) error
}

// Detect picks the backend for this system.
func Detect() Manager {
	if fi, err := os.Stat("/run/systemd/system"); err == nil && fi.IsDir() {
		return systemd{}
	}
	for _, p := range []string{"/sbin/openrc-run", "/usr/sbin/openrc-run", "/bin/openrc-run"} {
		if _, err := os.Stat(p); err == nil {
			return openrc{runner: p}
		}
	}
	return pidfile{}
}

func runArgs(bin string) []string {
	return []string{bin, "run", "-c", paths.SingBoxConfig, "-D", paths.LibDir}
}

func run(name string, args ...string) error {
	cmd := exec.Command(name, args...)
	cmd.Stdout, cmd.Stderr = os.Stdout, os.Stderr
	return cmd.Run()
}

// ---------- systemd ----------

type systemd struct{}

const unitPath = "/etc/systemd/system/" + paths.ServiceName + ".service"

func (systemd) Name() string { return "systemd" }

func (systemd) Install(bin string) error {
	unit := fmt.Sprintf(`[Unit]
Description=vpnctl (sing-box TUN proxy)
Documentation=https://sing-box.sagernet.org
After=network-online.target nss-lookup.target
Wants=network-online.target

[Service]
Type=simple
ExecStart=%s
ExecReload=/bin/kill -HUP $MAINPID
Restart=on-failure
RestartSec=5s
LimitNOFILE=infinity

[Install]
WantedBy=multi-user.target
`, strings.Join(runArgs(bin), " "))
	if err := settings.WriteFileAtomic(unitPath, []byte(unit), 0o644); err != nil {
		return err
	}
	if err := run("systemctl", "daemon-reload"); err != nil {
		return err
	}
	return run("systemctl", "enable", "-q", paths.ServiceName)
}

func (systemd) Uninstall() error {
	_ = run("systemctl", "disable", "-q", "--now", paths.ServiceName)
	if err := os.Remove(unitPath); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	return run("systemctl", "daemon-reload")
}

func (systemd) Start() error   { return run("systemctl", "start", paths.ServiceName) }
func (systemd) Stop() error    { return run("systemctl", "stop", paths.ServiceName) }
func (systemd) Restart() error { return run("systemctl", "restart", paths.ServiceName) }
func (systemd) Active() bool {
	return exec.Command("systemctl", "is-active", "-q", paths.ServiceName).Run() == nil
}
func (systemd) Logs(follow bool) error {
	args := []string{"-u", paths.ServiceName, "-n", "50", "--no-pager"}
	if follow {
		args = append(args, "-f")
	}
	return run("journalctl", args...)
}

// ---------- OpenRC ----------

type openrc struct{ runner string }

const initPath = "/etc/init.d/" + paths.ServiceName

func (openrc) Name() string { return "openrc" }

func (o openrc) Install(bin string) error {
	a := runArgs(bin)
	script := fmt.Sprintf(`#!%s
description="vpnctl (sing-box TUN proxy)"
command=%q
command_args=%q
command_background=true
pidfile=%q
output_log=%q
error_log=%q

depend() {
	need net
	after firewall
}
`, o.runner, a[0], strings.Join(a[1:], " "), paths.PidFile, paths.LogFile, paths.LogFile)
	if err := settings.WriteFileAtomic(initPath, []byte(script), 0o755); err != nil {
		return err
	}
	return run("rc-update", "add", paths.ServiceName, "default")
}

func (openrc) Uninstall() error {
	_ = run("rc-service", paths.ServiceName, "stop")
	_ = run("rc-update", "del", paths.ServiceName, "default")
	if err := os.Remove(initPath); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	return nil
}

func (openrc) Start() error   { return run("rc-service", paths.ServiceName, "start") }
func (openrc) Stop() error    { return run("rc-service", paths.ServiceName, "stop") }
func (openrc) Restart() error { return run("rc-service", paths.ServiceName, "restart") }
func (openrc) Active() bool {
	return exec.Command("rc-service", paths.ServiceName, "status").Run() == nil
}
func (openrc) Logs(follow bool) error { return tailLog(follow) }

// ---------- pid file (no init system integration) ----------

type pidfile struct{}

const binLink = paths.LibDir + "/run-bin"

func (pidfile) Name() string { return "pidfile (no autostart)" }

func (pidfile) Install(bin string) error {
	// Remember which binary to start.
	_ = os.Remove(binLink)
	return os.Symlink(bin, binLink)
}

func (p pidfile) Uninstall() error {
	_ = p.Stop()
	_ = os.Remove(binLink)
	return nil
}

func readPid() int {
	data, err := os.ReadFile(paths.PidFile)
	if err != nil {
		return 0
	}
	pid, _ := strconv.Atoi(strings.TrimSpace(string(data)))
	return pid
}

func alive(pid int) bool {
	if pid <= 0 || syscall.Kill(pid, 0) != nil {
		return false
	}
	comm, err := os.ReadFile(fmt.Sprintf("/proc/%d/comm", pid))
	return err != nil || strings.Contains(string(comm), "sing-box")
}

func (pidfile) Active() bool { return alive(readPid()) }

func (p pidfile) Start() error {
	if p.Active() {
		return nil
	}
	bin, err := os.Readlink(binLink)
	if err != nil {
		return fmt.Errorf("service not installed, run: vpnctl setup")
	}
	logf, err := os.OpenFile(paths.LogFile, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o640)
	if err != nil {
		return err
	}
	defer logf.Close()
	a := runArgs(bin)
	cmd := exec.Command(a[0], a[1:]...)
	cmd.Stdout, cmd.Stderr = logf, logf
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	if err := cmd.Start(); err != nil {
		return err
	}
	pid := cmd.Process.Pid
	_ = cmd.Process.Release()
	if err := os.WriteFile(paths.PidFile, []byte(strconv.Itoa(pid)+"\n"), 0o644); err != nil {
		return err
	}
	time.Sleep(1500 * time.Millisecond)
	if !alive(pid) {
		return fmt.Errorf("sing-box exited right after start, see: vpnctl logs")
	}
	return nil
}

func (p pidfile) Stop() error {
	pid := readPid()
	if !alive(pid) {
		_ = os.Remove(paths.PidFile)
		return nil
	}
	_ = syscall.Kill(pid, syscall.SIGTERM)
	for i := 0; i < 50 && alive(pid); i++ {
		time.Sleep(100 * time.Millisecond)
	}
	if alive(pid) {
		_ = syscall.Kill(pid, syscall.SIGKILL)
	}
	return os.Remove(paths.PidFile)
}

func (p pidfile) Restart() error {
	if err := p.Stop(); err != nil {
		return err
	}
	return p.Start()
}

func (pidfile) Logs(follow bool) error { return tailLog(follow) }

func tailLog(follow bool) error {
	args := []string{"-n", "50"}
	if follow {
		args = append(args, "-f")
	}
	return run("tail", append(args, paths.LogFile)...)
}

// ConflictingUnits lists other active sing-box services that would fight over TUN.
func ConflictingUnits() []string {
	if _, ok := Detect().(systemd); !ok {
		return nil
	}
	var out []string
	for _, u := range []string{"sing-box.service", "sing-box@config.service"} {
		if exec.Command("systemctl", "is-active", "-q", u).Run() == nil {
			out = append(out, u)
		}
	}
	return out
}
