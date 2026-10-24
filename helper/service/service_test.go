package service

import (
	"errors"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"
)

func TestFilesDarwin(t *testing.T) {
	files, err := filesFor("darwin", "/Users/me", "/opt/a & b/ailimits", "")
	if err != nil || len(files) != 1 {
		t.Fatal(files, err)
	}
	body := files[filepath.Join("/Users/me", "Library", "LaunchAgents", "ru.second-brain.ailimits.plist")]
	for _, want := range []string{
		"<string>/opt/a &amp; b/ailimits</string>", "<string>run</string>",
		"<key>RunAtLoad</key>", "<key>KeepAlive</key>", "<string>ru.second-brain.ailimits</string>",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("plist lacks %q:\n%s", want, body)
		}
	}
}

func TestFilesLinux(t *testing.T) {
	files, err := filesFor("linux", "/home/me", "/home/me/bin/ailimits", "")
	if err != nil || len(files) != 1 {
		t.Fatal(files, err)
	}
	body := files[filepath.Join("/home/me", ".config", "systemd", "user", "ailimits.service")]
	for _, want := range []string{"ExecStart=/home/me/bin/ailimits run\n", "Restart=on-failure", "WantedBy=default.target"} {
		if !strings.Contains(body, want) {
			t.Errorf("unit lacks %q:\n%s", want, body)
		}
	}
	files, _ = filesFor("linux", "/home/me", "/home/my bin/ailimits", "")
	for _, b := range files {
		if !strings.Contains(b, `ExecStart="/home/my bin/ailimits" run`) {
			t.Errorf("path with space not quoted:\n%s", b)
		}
	}
}

func TestFilesWindowsHasNoFile(t *testing.T) {
	files, err := filesFor("windows", `C:\Users\me`, `C:\x\ailimits.exe`, "")
	if err != nil || len(files) != 0 {
		t.Fatal(files, err)
	}
}

func TestFilesUnsupported(t *testing.T) {
	if _, err := filesFor("plan9", "/", "/x", ""); err == nil {
		t.Fatal("want error")
	}
}

func TestRegArgs(t *testing.T) {
	got := regAddArgs(`C:\Program Files\AI Limits\ailimits.exe`, "")
	want := []string{"add", `HKCU\Software\Microsoft\Windows\CurrentVersion\Run`, "/v", "AILimits", "/t", "REG_SZ", "/d",
		`conhost.exe --headless "C:\Program Files\AI Limits\ailimits.exe" run`, "/f"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("\n got %q\nwant %q", got, want)
	}
	del := regDeleteArgs()
	wantDel := []string{"delete", `HKCU\Software\Microsoft\Windows\CurrentVersion\Run`, "/v", "AILimits", "/f"}
	if !reflect.DeepEqual(del, wantDel) {
		t.Fatal(del)
	}
}

func TestStablePath(t *testing.T) {
	has := func(p string) bool { return true }
	none := func(p string) bool { return false }
	for _, prefix := range []string{"/opt/homebrew", "/usr/local", "/home/linuxbrew/.linuxbrew"} {
		exe := prefix + "/Cellar/ailimits/1.0.0/bin/ailimits"
		if got := StablePath(exe, has); got != prefix+"/bin/ailimits" {
			t.Errorf("%s -> %s", exe, got)
		}
		if got := StablePath(exe, none); got != exe {
			t.Errorf("missing stable path must keep exe, got %s", got)
		}
	}
	if got := StablePath("/home/me/bin/ailimits", has); got != "/home/me/bin/ailimits" {
		t.Fatal(got)
	}
}

func TestDarwinLoadRetriesBootstrap(t *testing.T) {
	oldRun, oldSleep := runCmd, sleep
	defer func() { runCmd, sleep = oldRun, oldSleep }()
	var calls []string
	bootstraps, sleeps := 0, 0
	runCmd = func(name string, args ...string) error {
		calls = append(calls, args[0])
		if args[0] == "bootstrap" {
			bootstraps++
			if bootstraps < 3 {
				return errors.New("launchctl failed: Bootstrap failed: 5: Input/output error")
			}
		}
		return nil
	}
	sleep = func(time.Duration) { sleeps++ }
	if err := darwinLoad(map[string]string{"/p.plist": ""}); err != nil {
		t.Fatal(err)
	}
	if bootstraps != 3 || sleeps != 2 || calls[0] != "bootout" {
		t.Fatal(calls, sleeps)
	}
	bootstraps = -100 // always fail
	err := darwinLoad(map[string]string{"/p.plist": ""})
	if err == nil || !strings.Contains(err.Error(), "Input/output error") {
		t.Fatal(err)
	}
	n := 0
	for _, c := range calls {
		if c == "bootstrap" {
			n++
		}
	}
	if n != 3+5 {
		t.Fatal("bootstrap attempts", n)
	}
}

func TestRunCmdKeepsFirstStderrLine(t *testing.T) {
	err := runCmd("sh", "-c", "printf '\\nfirst problem\\nsecond\\n' >&2; exit 1")
	if err == nil || err.Error() != "sh failed: first problem" {
		t.Fatal(err)
	}
}

func TestTaskkillExcludesSelf(t *testing.T) {
	want := []string{"/F", "/IM", "ailimits.exe", "/FI", "PID ne 4242"}
	if got := taskkillArgs(4242); !reflect.DeepEqual(got, want) {
		t.Fatal(got)
	}
}

func TestWindowsInstallOrder(t *testing.T) {
	oldRun, oldStart, oldPid := runCmd, startDetached, getpid
	defer func() { runCmd, startDetached, getpid = oldRun, oldStart, oldPid }()
	getpid = func() int { return 77 }
	var seq []string
	runCmd = func(name string, args ...string) error {
		seq = append(seq, name+" "+strings.Join(args, "|"))
		if name == "taskkill" {
			return errors.New("not running") // must be ignored
		}
		return nil
	}
	startDetached = func(name string, args ...string) error {
		seq = append(seq, "start "+name+" "+strings.Join(args, "|"))
		return nil
	}
	if err := windowsInstall(`C:\a b\ailimits.exe`, ""); err != nil {
		t.Fatal(err)
	}
	if len(seq) != 3 ||
		!strings.HasPrefix(seq[0], "reg add|") ||
		seq[1] != "taskkill /F|/IM|ailimits.exe|/FI|PID ne 77" ||
		seq[2] != `start conhost.exe --headless|C:\a b\ailimits.exe|run` {
		t.Fatal(seq)
	}
}

func TestMissing(t *testing.T) {
	files := map[string]string{"/a": "", "/b": ""}
	if missing(files, func(string) bool { return true }) {
		t.Fatal("all present: want not missing")
	}
	if !missing(files, func(p string) bool { return p == "/a" }) {
		t.Fatal("one absent: want missing")
	}
	if missing(map[string]string{}, func(string) bool { return false }) {
		t.Fatal("no files (Windows): want not missing")
	}
}

func TestFilesCarryClaudeConfigDir(t *testing.T) {
	files, _ := filesFor("darwin", "/Users/me", "/usr/local/bin/ailimits", "/Users/me/.claude work")
	for _, b := range files {
		for _, want := range []string{"<string>--claude-config-dir</string>", "<string>/Users/me/.claude work</string>"} {
			if !strings.Contains(b, want) {
				t.Errorf("plist lacks %q", want)
			}
		}
	}
	files, _ = filesFor("darwin", "/Users/me", "/usr/local/bin/ailimits", "")
	for _, b := range files {
		if strings.Contains(b, "--claude-config-dir") {
			t.Error("flag present without a config dir")
		}
	}
	files, _ = filesFor("linux", "/home/me", "/home/me/bin/ailimits", "/home/me/.claude work")
	for _, b := range files {
		if !strings.Contains(b, `ExecStart=/home/me/bin/ailimits run --claude-config-dir "/home/me/.claude work"`+"\n") {
			t.Errorf("unit:\n%s", b)
		}
	}
}

func TestWindowsRunValueCarriesConfigDir(t *testing.T) {
	got := regAddArgs(`C:\ai\ailimits.exe`, `C:\Users\me\.claude work`)
	want := `conhost.exe --headless "C:\ai\ailimits.exe" run --claude-config-dir "C:\Users\me\.claude work"`
	if got[7] != want {
		t.Fatalf("\n got %q\nwant %q", got[7], want)
	}
}

func TestSystemdEscapesDollar(t *testing.T) {
	files, _ := filesFor("linux", "/home/me", "/home/me/$bin/ailimits", "")
	for _, b := range files {
		if !strings.Contains(b, "/home/me/$$bin/ailimits") {
			t.Errorf("dollar not escaped:\n%s", b)
		}
	}
}
