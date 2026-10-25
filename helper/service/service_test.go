package service

import (
	"errors"
	"fmt"
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

func TestRunValue(t *testing.T) {
	got := windowsRunValue(`C:\Program Files\AI Limits\ailimitsw.exe`, "")
	want := `"C:\Program Files\AI Limits\ailimitsw.exe" run`
	if got != want {
		t.Fatalf("\n got %q\nwant %q", got, want)
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

func TestHelpersToStopSkipsSelfAndOtherPrograms(t *testing.T) {
	procs := []proc{{1, "ailimits.exe"}, {2, "AILimits.EXE"}, {77, "ailimits.exe"}, {3, "conhost.exe"}, {4, "ailimitsw.exe"}}
	if got := helpersToStop(procs, 77); !reflect.DeepEqual(got, []int{1, 2, 4}) {
		t.Fatal(got)
	}
}

// Windows Defender flags `reg.exe add …\Run` started by an unsigned program as
// Trojan:Win32/Commando.A!ml and kills it; taskkill /F looks the same. Install
// must use the registry and process APIs, not spawn those tools.
func windowsFakes(t *testing.T) *[]string {
	oldRun, oldStart, oldPid := runCmd, startDetached, getpid
	oldSet, oldDel, oldList, oldKill := setRunValue, deleteRunValue, listProcs, killPid
	oldSleep, oldExists := sleep, fileExists
	sleep = func(time.Duration) {}
	fileExists = func(string) bool { return true }
	t.Cleanup(func() {
		runCmd, startDetached, getpid, sleep, fileExists = oldRun, oldStart, oldPid, oldSleep, oldExists
		setRunValue, deleteRunValue, listProcs, killPid = oldSet, oldDel, oldList, oldKill
	})
	var seq []string
	getpid = func() int { return 77 }
	runCmd = func(name string, args ...string) error {
		seq = append(seq, "spawn "+name)
		return nil
	}
	setRunValue = func(v string) error { seq = append(seq, "set "+v); return nil }
	deleteRunValue = func() error { seq = append(seq, "delete"); return nil }
	listProcs = func() ([]proc, error) { return []proc{{5, "ailimits.exe"}, {77, "ailimits.exe"}}, nil }
	killPid = func(pid int) error { seq = append(seq, fmt.Sprintf("kill %d", pid)); return errors.New("gone") }
	startDetached = func(name string, args ...string) error {
		seq = append(seq, "start "+name+" "+strings.Join(args, "|"))
		return nil
	}
	return &seq
}

func TestWindowsInstallOrder(t *testing.T) {
	seq := windowsFakes(t)
	if err := windowsInstall(`C:\a b\ailimits.exe`, ""); err != nil {
		t.Fatal(err)
	}
	want := []string{
		`set "C:\a b\ailimitsw.exe" run`,
		"kill 5", // a failed kill is ignored
		`start C:\a b\ailimitsw.exe run`,
	}
	if !reflect.DeepEqual(*seq, want) {
		t.Fatalf("\n got %q\nwant %q", *seq, want)
	}
}

func TestWindowsInstallStopsWhenRunValueFails(t *testing.T) {
	seq := windowsFakes(t)
	setRunValue = func(string) error { return errors.New("cannot write the Run key: denied") }
	if err := windowsInstall(`C:\ai\ailimits.exe`, ""); err == nil || !strings.Contains(err.Error(), "Run key") {
		t.Fatalf("err %v", err)
	}
	if len(*seq) != 0 {
		t.Fatalf("nothing may run after a failed write: %q", *seq)
	}
}

func TestWindowsUninstallUsesAPIs(t *testing.T) {
	seq := windowsFakes(t)
	windowsUninstall()
	if want := []string{"delete", "kill 5"}; !reflect.DeepEqual(*seq, want) {
		t.Fatalf("\n got %q\nwant %q", *seq, want)
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
	got := windowsRunValue(`C:\ai\ailimitsw.exe`, `C:\Users\me\.claude work`)
	want := `"C:\ai\ailimitsw.exe" run --claude-config-dir "C:\Users\me\.claude work"`
	if got != want {
		t.Fatalf("\n got %q\nwant %q", got, want)
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

// The helper started by install-service must outlive the terminal (and the
// ssh session) it was started from: detached from the console, outside the job.
func TestStartOutsideBreaksAwayFromJob(t *testing.T) {
	var got []uint32
	err := startOutside(func(flags uint32) error { got = append(got, flags); return nil })
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0] != 0x00000008|0x00000200|0x01000000 {
		t.Fatalf("flags %#x", got)
	}
}

func TestStartOutsideRetriesWhenJobForbidsBreakaway(t *testing.T) {
	var got []uint32
	err := startOutside(func(flags uint32) error {
		got = append(got, flags)
		if flags&0x01000000 != 0 {
			return errors.New("Access is denied.")
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || got[1] != 0x00000008|0x00000200 {
		t.Fatalf("flags %#x", got)
	}
}

func TestStartOutsideReportsSecondFailure(t *testing.T) {
	err := startOutside(func(uint32) error { return errors.New("no conhost") })
	if err == nil || err.Error() != "no conhost" {
		t.Fatalf("err %v", err)
	}
}

// On a fresh download the helper started by install-service sometimes does not
// stay running (seen on Windows 11 with Defender). Install checks and retries.
func TestWindowsInstallRetriesUntilHelperRuns(t *testing.T) {
	seq := windowsFakes(t)
	sleep = func(time.Duration) {}
	calls := 0
	listProcs = func() ([]proc, error) {
		calls++
		if calls < 3 { // stopHelpers, then the first check: only this process
			return []proc{{77, "ailimits.exe"}}, nil
		}
		return []proc{{77, "ailimits.exe"}, {9, "ailimits.exe"}}, nil
	}
	if err := windowsInstall(`C:\ai\ailimits.exe`, ""); err != nil {
		t.Fatal(err)
	}
	starts := 0
	for _, s := range *seq {
		if strings.HasPrefix(s, "start ") {
			starts++
		}
	}
	if starts != 2 {
		t.Fatalf("want 2 starts, got %q", *seq)
	}
}

func TestWindowsInstallReportsHelperNotRunning(t *testing.T) {
	seq := windowsFakes(t)
	sleep = func(time.Duration) {}
	listProcs = func() ([]proc, error) { return []proc{{77, "ailimits.exe"}}, nil }
	err := windowsInstall(`C:\ai\ailimits.exe`, "")
	if !errors.Is(err, ErrNotRunningYet) {
		t.Fatalf("err %v", err)
	}
	starts := 0
	for _, s := range *seq {
		if strings.HasPrefix(s, "start ") {
			starts++
		}
	}
	if starts != 3 || !strings.HasPrefix((*seq)[0], "set ") {
		t.Fatalf("want the Run value and 3 starts, got %q", *seq)
	}
}

// The background helper is the GUI-subsystem build next to ailimits.exe: no
// console, so no window and no conhost that can close it (exit 0xC000013A).
func TestBackgroundExe(t *testing.T) {
	for in, want := range map[string]string{
		`C:\a b\ailimits.exe`: `C:\a b\ailimitsw.exe`,
		`C:/ai/AILIMITS.EXE`:  `C:/ai/ailimitsw.exe`,
		`ailimits.exe`:        `ailimitsw.exe`,
	} {
		if got := backgroundExe(in); got != want {
			t.Errorf("%s -> %s, want %s", in, got, want)
		}
	}
}

func TestWindowsInstallNeedsBackgroundExe(t *testing.T) {
	seq := windowsFakes(t)
	fileExists = func(string) bool { return false }
	err := windowsInstall(`C:\ai\ailimits.exe`, "")
	if err == nil || !strings.Contains(err.Error(), "ailimitsw.exe") {
		t.Fatalf("err %v", err)
	}
	if len(*seq) != 0 {
		t.Fatalf("nothing may change: %q", *seq)
	}
}
