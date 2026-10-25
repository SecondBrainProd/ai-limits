// Package service installs the helper as a per-user autostart job:
// launchd (macOS), systemd --user (Linux) or the per-user Run registry key (Windows).
package service

import (
	"bytes"
	"encoding/xml"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"time"
)

const (
	label    = "ru.second-brain.ailimits"
	unitName = "ailimits.service"
	runKey   = `Software\Microsoft\Windows\CurrentVersion\Run` // under HKEY_CURRENT_USER
	runValue = "AILimits"
)

// Files returns the autostart files to write (path -> content) for this OS.
// Windows needs no file: it uses the per-user Run registry key.
func Files(exe, claudeConfigDir string) (map[string]string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return nil, errors.New("cannot find your home directory")
	}
	return filesFor(runtime.GOOS, home, exe, claudeConfigDir)
}

// Missing reports whether no autostart is installed. It checks only the
// service file on macOS and Linux; on Windows (no file) and on unsupported
// systems it reports false, so no hint is shown.
func Missing() bool {
	files, err := Files("", "")
	if err != nil {
		return false
	}
	return missing(files, func(p string) bool { _, e := os.Stat(p); return e == nil })
}

func missing(files map[string]string, exists func(string) bool) bool {
	for p := range files {
		if !exists(p) {
			return true
		}
	}
	return false
}

// runArgs are the arguments after the program path: "run" plus the Claude Code
// profile chosen at install time, so the service reads the same profile as the
// terminal did.
func runArgs(claudeConfigDir string) []string {
	if claudeConfigDir == "" {
		return []string{"run"}
	}
	return []string{"run", "--claude-config-dir", claudeConfigDir}
}

func filesFor(goos, home, exe, claudeConfigDir string) (map[string]string, error) {
	switch goos {
	case "darwin":
		return map[string]string{
			filepath.Join(home, "Library", "LaunchAgents", label+".plist"): plist(exe, runArgs(claudeConfigDir), filepath.Join(home, "Library", "Logs", "ailimits.log")),
		}, nil
	case "linux":
		return map[string]string{
			filepath.Join(home, ".config", "systemd", "user", unitName): unit(exe, runArgs(claudeConfigDir)),
		}, nil
	case "windows":
		return map[string]string{}, nil
	}
	return nil, fmt.Errorf("autostart is not supported on %s", goos)
}

func esc(s string) string {
	var b strings.Builder
	_ = xml.EscapeText(&b, []byte(s))
	return b.String()
}

func plist(exe string, args []string, logPath string) string {
	var a strings.Builder
	for _, x := range args {
		a.WriteString("\t\t<string>" + esc(x) + "</string>\n")
	}
	return `<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
	<key>Label</key>
	<string>` + label + `</string>
	<key>ProgramArguments</key>
	<array>
		<string>` + esc(exe) + `</string>
` + a.String() + `	</array>
	<key>RunAtLoad</key>
	<true/>
	<key>KeepAlive</key>
	<true/>
	<key>StandardOutPath</key>
	<string>` + esc(logPath) + `</string>
	<key>StandardErrorPath</key>
	<string>` + esc(logPath) + `</string>
</dict>
</plist>
`
}

// systemdArg escapes one ExecStart word: %, $ and quoting for spaces.
func systemdArg(v string) string {
	p := strings.NewReplacer("%", "%%", "$", "$$").Replace(v)
	if strings.ContainsAny(p, " \t\"'\\") {
		p = `"` + strings.NewReplacer(`\`, `\\`, `"`, `\"`).Replace(p) + `"`
	}
	return p
}

func unit(exe string, args []string) string {
	words := []string{systemdArg(exe)}
	for _, a := range args {
		words = append(words, systemdArg(a))
	}
	p := strings.Join(words, " ")
	return `[Unit]
Description=AI Limits helper (shows Claude Code limits on your Garmin watch)

[Service]
ExecStart=` + p + `
Restart=on-failure
RestartSec=30

[Install]
WantedBy=default.target
`
}

// backgroundExe is the GUI-subsystem build that ships next to ailimits.exe. It
// has no console, so no window, and nothing can close it under the helper:
// `conhost --headless` used to, now and then (exit 0xC000013A).
func backgroundExe(exe string) string {
	return exe[:strings.LastIndexAny(exe, `\/`)+1] + "ailimitsw.exe"
}

// windowsRunValue is the Run-key command; bg is the background exe.
func windowsRunValue(bg, claudeConfigDir string) string {
	v := `"` + bg + `" run`
	if claudeConfigDir != "" {
		v += ` --claude-config-dir "` + claudeConfigDir + `"`
	}
	return v
}

// StablePath returns a path that survives upgrades. Homebrew installs the real
// binary under <prefix>/Cellar/ailimits/<version>/, which `brew upgrade`
// deletes; <prefix>/bin/ailimits always points at the current version.
func StablePath(exe string, exists func(string) bool) string {
	const marker = "/Cellar/ailimits/"
	i := strings.Index(exe, marker)
	if i < 0 {
		return exe
	}
	stable := exe[:i] + "/bin/ailimits"
	if exists(stable) {
		return stable
	}
	return exe
}

// runCmd is replaced in tests. On failure the error keeps the first non-empty
// stderr line, so the user sees what to fix.
var runCmd = func(name string, args ...string) error {
	var stderr bytes.Buffer
	cmd := exec.Command(name, args...)
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		for _, line := range strings.Split(stderr.String(), "\n") {
			if line = strings.TrimSpace(line); line != "" {
				return fmt.Errorf("%s failed: %s", name, line)
			}
		}
		return fmt.Errorf("%s failed: %v", name, err)
	}
	return nil
}

// Windows process creation flags (winbase.h).
const (
	detachedProcess        = 0x00000008
	createNewProcessGroup  = 0x00000200
	createBreakawayFromJob = 0x01000000
)

// startOutside starts the helper so it outlives the terminal or ssh session
// that ran install-service: no console of its own to be closed with the window,
// and outside the session's job object. A job that forbids breakaway refuses
// the first try; the second one still detaches from the console.
func startOutside(launch func(flags uint32) error) error {
	flags := uint32(detachedProcess | createNewProcessGroup)
	if err := launch(flags | createBreakawayFromJob); err == nil {
		return nil
	}
	return launch(flags)
}

// startDetached is replaced in tests.
var startDetached = func(name string, args ...string) error {
	err := startOutside(func(flags uint32) error {
		cmd := exec.Command(name, args...)
		cmd.SysProcAttr = detachAttr(flags)
		if err := cmd.Start(); err != nil {
			return err
		}
		go cmd.Wait()
		return nil
	})
	if err != nil {
		return fmt.Errorf("cannot start %s: %v", name, err)
	}
	return nil
}

var sleep = time.Sleep

var getpid = os.Getpid

// The Run key and the running helpers are handled through Windows APIs, not
// reg.exe and taskkill: Defender flags `reg.exe add …\Run` started by an
// unsigned program as Trojan:Win32/Commando.A!ml. Replaced in tests.
var (
	setRunValue    = setRunValueOS
	deleteRunValue = deleteRunValueOS
	listProcs      = listProcsOS
	killPid        = killPidOS
)

type proc struct {
	pid  int
	name string
}

// helpersToStop picks every ailimits.exe and ailimitsw.exe except this process.
func helpersToStop(procs []proc, self int) []int {
	var pids []int
	for _, p := range procs {
		if p.pid != self && (strings.EqualFold(p.name, "ailimits.exe") || strings.EqualFold(p.name, "ailimitsw.exe")) {
			pids = append(pids, p.pid)
		}
	}
	return pids
}

func stopHelpers() {
	procs, err := listProcs()
	if err != nil {
		return
	}
	for _, pid := range helpersToStop(procs, getpid()) {
		_ = killPid(pid) // already gone is fine
	}
}

// ErrNotRunningYet: autostart is set, but the helper started now did not stay
// running. It starts at the next sign-in.
var ErrNotRunningYet = errors.New("the helper did not stay running")

// helperRunning reports whether another ailimits.exe runs; true when unknown.
func helperRunning() bool {
	procs, err := listProcs()
	return err != nil || len(helpersToStop(procs, getpid())) > 0
}

// fileExists is replaced in tests.
var fileExists = func(p string) bool { _, err := os.Stat(p); return err == nil }

func windowsInstall(exe, claudeConfigDir string) error {
	bg := backgroundExe(exe)
	if !fileExists(bg) {
		return errors.New("ailimitsw.exe is missing next to ailimits.exe: unzip the whole archive into one folder")
	}
	if err := setRunValue(windowsRunValue(bg, claudeConfigDir)); err != nil {
		return err
	}
	stopHelpers() // no second helper after a reinstall
	// Safety net: check that the started helper stays running, and retry.
	for try := 0; try < 3; try++ {
		if err := startDetached(bg, runArgs(claudeConfigDir)...); err != nil {
			return err
		}
		sleep(2 * time.Second)
		if helperRunning() {
			return nil
		}
	}
	return ErrNotRunningYet
}

func windowsUninstall() {
	_ = deleteRunValue() // fine if the value is not there
	stopHelpers()
}

// Install writes the autostart entry and starts the helper now.
func Install(exe, claudeConfigDir string) error {
	files, err := Files(exe, claudeConfigDir)
	if err != nil {
		return err
	}
	for path, body := range files {
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			return errors.New("cannot create " + filepath.Dir(path))
		}
		if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
			return errors.New("cannot write " + path)
		}
	}
	switch runtime.GOOS {
	case "darwin":
		return darwinLoad(files)
	case "linux":
		if err := runCmd("systemctl", "--user", "daemon-reload"); err != nil {
			return err
		}
		return runCmd("systemctl", "--user", "enable", "--now", unitName)
	case "windows":
		return windowsInstall(exe, claudeConfigDir)
	}
	return nil
}

func darwinLoad(files map[string]string) error {
	domain := "gui/" + strconv.Itoa(os.Getuid())
	for path := range files {
		_ = runCmd("launchctl", "bootout", domain+"/"+label) // fine if not loaded yet
		var err error
		// bootout is asynchronous: bootstrap can fail briefly right after it.
		for try := 0; try < 5; try++ {
			if err = runCmd("launchctl", "bootstrap", domain, path); err == nil {
				break
			}
			sleep(500 * time.Millisecond)
		}
		if err != nil {
			return err
		}
	}
	return nil
}

// Uninstall stops the helper and removes the autostart entry.
func Uninstall() error {
	files, err := Files("", "")
	if err != nil {
		return err
	}
	switch runtime.GOOS {
	case "darwin":
		_ = runCmd("launchctl", "bootout", "gui/"+strconv.Itoa(os.Getuid())+"/"+label)
	case "linux":
		_ = runCmd("systemctl", "--user", "disable", "--now", unitName)
	case "windows":
		windowsUninstall()
	}
	for path := range files {
		if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
			return errors.New("cannot remove " + path)
		}
	}
	if runtime.GOOS == "linux" {
		_ = runCmd("systemctl", "--user", "daemon-reload")
	}
	return nil
}
