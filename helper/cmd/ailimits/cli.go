package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/SecondBrainProd/ai-limits/helper/agent"
	"github.com/SecondBrainProd/ai-limits/helper/config"
	"github.com/SecondBrainProd/ai-limits/helper/creds"
	"github.com/SecondBrainProd/ai-limits/helper/relayclient"
	"github.com/SecondBrainProd/ai-limits/helper/service"
	"github.com/SecondBrainProd/ai-limits/helper/usage"
	"github.com/SecondBrainProd/ai-limits/internal/snapshot"
)

const (
	// 5 minutes: the watch fetches every 5 minutes anyway (the Connect IQ minimum),
	// and Anthropic rate-limits the usage endpoint.
	cycle = 5 * time.Minute

	msgBadCode = "That code is invalid or expired. Open AI Limits on your watch for a fresh one."
	usageText  = `AI Limits - show your Claude Code limits on a Garmin watch

Usage: ailimits <command>

Commands:
  pair <CODE>        Pair this computer with your watch (code is shown on the watch)
  once               Send your current limits once
  run                Send your limits every 3 minutes (the autostart service runs this)
  status             Show whether this computer is paired and your current limits
  unpair             Forget the pairing on this computer
  install-service    Start AI Limits automatically when you log in
  uninstall-service  Remove the autostart
  version            Print the version
  help               Show this help
`
)

type pairer interface {
	Pair(ctx context.Context, code, helperToken string) (string, error)
	Forget(ctx context.Context, helperToken string) error
}

// cli holds the command dependencies so tests can replace them.
type cli struct {
	out, err io.Writer
	newRelay func(config.Config) pairer
	newAgent func(config.Config) agent.Agent
	now      func() time.Time
	poll     time.Duration // how often `run` checks the config for a new pairing
	every    time.Duration // cycle length of `run`
	// autostartMissing reports whether no autostart is installed (nil: unknown,
	// so no hint is printed).
	autostartMissing func() bool
}

func userAgent() string {
	return usage.UserAgentPrefix + version + " (+https://github.com/SecondBrainProd/ai-limits)"
}

func newCLI() *cli {
	return &cli{
		out: os.Stdout, err: os.Stderr,
		newRelay: func(cfg config.Config) pairer {
			return relayclient.Client{Base: cfg.Relay, UserAgent: userAgent()}
		},
		autostartMissing: service.Missing,
		newAgent: func(cfg config.Config) agent.Agent {
			return agent.Agent{
				Creds:  creds.Default(),
				Usage:  usage.Client{Version: version},
				Relay:  relayclient.Client{Base: cfg.Relay, UserAgent: userAgent()},
				Config: cfg,
				Log:    os.Stderr,
			}
		},
		now:   time.Now,
		poll:  15 * time.Second,
		every: cycle,
	}
}

func (c *cli) printf(w io.Writer, format string, args ...any) { fmt.Fprintf(w, format+"\n", args...) }

func (c *cli) run(ctx context.Context, args []string) int {
	cmd := "help"
	if len(args) > 0 {
		cmd = args[0]
	}
	rest := []string{}
	if len(args) > 1 {
		rest = args[1:]
	}
	switch cmd {
	case "help", "-h", "--help":
		fmt.Fprint(c.out, usageText)
		return 0
	case "version", "-v", "--version":
		c.printf(c.out, "ailimits %s", version)
		return 0
	case "pair":
		return c.pair(ctx, rest)
	case "once":
		return c.once(ctx)
	case "run":
		dir, err := parseRunArgs(rest)
		if err != nil {
			c.printf(c.err, "%v", err)
			return 2
		}
		if dir != "" {
			// The service passes the Claude Code profile chosen at install time.
			os.Setenv("CLAUDE_CONFIG_DIR", dir)
		}
		return c.daemon(ctx)
	case "status":
		return c.status(ctx)
	case "unpair":
		return c.unpair(ctx)
	case "install-service":
		return c.installService()
	case "uninstall-service":
		return c.uninstallService()
	}
	c.printf(c.err, "Unknown command %q. Run: ailimits help", cmd)
	return 2
}

// normalizeCode upper-cases a pairing code and drops spaces and dashes.
func normalizeCode(s string) string {
	return strings.ToUpper(strings.NewReplacer(" ", "", "-", "", "\t", "").Replace(s))
}

func (c *cli) loadConfig() (config.Config, bool) {
	cfg, err := config.Load()
	if err != nil {
		c.printf(c.err, "%v. Delete %s and pair again.", err, config.Path())
		return cfg, false
	}
	return cfg, true
}

func (c *cli) pair(ctx context.Context, args []string) int {
	code := normalizeCode(strings.Join(args, ""))
	if code == "" {
		c.printf(c.err, "Usage: ailimits pair <CODE>   (the code is shown on your watch)")
		return 2
	}
	cfg, ok := c.loadConfig()
	if !ok {
		return 1
	}
	rc := c.newRelay(cfg)
	token, err := rc.Pair(ctx, code, cfg.HelperToken)
	if err != nil && cfg.HelperToken != "" && errors.Is(err, relayclient.ErrUnauthorized) {
		// The relay forgot the saved token: register as a new helper, once.
		token, err = rc.Pair(ctx, code, "")
	}
	switch {
	case errors.Is(err, relayclient.ErrBadCode):
		c.printf(c.err, "%s", msgBadCode)
		return 1
	case errors.Is(err, relayclient.ErrUnauthorized):
		// No saved token (or the retry without it failed): "no longer paired" would be wrong.
		c.printf(c.err, "Pairing failed. Try again with a fresh code from your watch.")
		return 1
	case err != nil:
		c.printf(c.err, "%v", err)
		return 1
	}
	cfg.HelperToken = token
	if err := config.Save(cfg); err != nil {
		c.printf(c.err, "Could not save the pairing: %v", err)
		return 1
	}
	c.printf(c.out, "Paired. Your watch will show limits within 5 minutes.")
	res, err := c.newAgent(cfg).Once(ctx)
	switch {
	case err != nil:
		c.printf(c.err, "The first update failed: %v. It will be retried automatically if the service is installed.", err)
	case res.Skipped != "":
		c.printf(c.err, "Nothing sent yet: %s", res.Skipped)
	}
	if c.autostartMissing != nil && c.autostartMissing() {
		c.printf(c.out, "Next: run `ailimits install-service` so limits keep updating.")
	}
	return 0
}

func (c *cli) once(ctx context.Context) int {
	cfg, ok := c.loadConfig()
	if !ok {
		return 1
	}
	res, err := c.newAgent(cfg).Once(ctx)
	switch {
	case err != nil:
		c.printf(c.err, "%v", err)
		return 1
	case res.Skipped != "":
		c.printf(c.err, "Nothing sent: %s", res.Skipped)
		return 1
	}
	c.printf(c.out, "Sent %d limits.", len(res.Rows))
	return 0
}

func (c *cli) status(ctx context.Context) int {
	cfg, ok := c.loadConfig()
	if !ok {
		return 1
	}
	if cfg.HelperToken == "" {
		c.printf(c.out, "Paired: no (run: ailimits pair <CODE>)")
	} else {
		c.printf(c.out, "Paired: yes")
	}
	res, err := c.newAgent(cfg).Preview(ctx)
	switch {
	case err != nil:
		c.printf(c.out, "Could not read your limits: %v", err)
	case res.Skipped != "":
		c.printf(c.out, "%s", res.Skipped)
	default:
		for _, l := range formatRows(res.Rows, c.now()) {
			c.printf(c.out, "%s", l)
		}
	}
	return 0
}

func (c *cli) unpair(ctx context.Context) int {
	cfg, ok := c.loadConfig()
	if !ok {
		return 1
	}
	if cfg.HelperToken == "" {
		c.printf(c.out, "This computer is not paired.")
		return 0
	}
	// Tell the relay first so the watch shows a fresh code instead of grey data;
	// unpair locally even if the relay cannot be reached.
	ferr := c.newRelay(cfg).Forget(ctx, cfg.HelperToken)
	cfg.HelperToken = ""
	if err := config.Save(cfg); err != nil {
		c.printf(c.err, "Could not update the config: %v", err)
		return 1
	}
	if ferr != nil && !errors.Is(ferr, relayclient.ErrUnauthorized) {
		c.printf(c.out, "Could not reach the relay; this computer is unpaired locally.")
		return 0
	}
	c.printf(c.out, "Unpaired. This computer no longer sends limits.")
	return 0
}

// parseRunArgs reads the flags of `ailimits run` (only --claude-config-dir DIR).
func parseRunArgs(args []string) (string, error) {
	dir := ""
	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--claude-config-dir":
			if i+1 >= len(args) || args[i+1] == "" {
				return "", errors.New("--claude-config-dir needs a directory")
			}
			dir = args[i+1]
			i++
		default:
			return "", fmt.Errorf("unknown option for run: %s", args[i])
		}
	}
	return dir, nil
}

// daemon is `ailimits run`: one cycle now, then every 3 minutes until stopped.
// It re-reads the config so a pairing done while it runs takes effect at once.
func (c *cli) daemon(ctx context.Context) int {
	// One log line per state change, not one every cycle.
	log := agent.NewChangeLog(c.err)
	logLine := func(format string, args ...any) {
		fmt.Fprintf(log, c.now().UTC().Format("2006-01-02T15:04:05Z")+" "+format+"\n", args...)
	}
	for ctx.Err() == nil {
		cfg, err := config.Load()
		if err != nil {
			logLine("error: %v", err)
			select {
			case <-ctx.Done():
			case <-time.After(c.poll):
			}
			continue
		}
		inner, cancel := context.WithCancel(ctx)
		go c.watchConfig(inner, cancel, cfg)
		a := c.newAgent(cfg)
		a.Log = log
		res, err := a.Once(inner)
		switch {
		case inner.Err() != nil:
		case errors.Is(err, relayclient.ErrUnauthorized):
			logLine("%v", err)
		case err != nil:
			logLine("error: %v", err)
		case res.Skipped != "":
			logLine("skipped: %s", res.Skipped)
		default:
			logLine("sent")
		}
		if inner.Err() == nil {
			_ = a.Run(inner, c.every)
		}
		cancel()
	}
	return 0
}

// watchConfig cancels the cycle when the relay or helper token changes.
func (c *cli) watchConfig(ctx context.Context, cancel context.CancelFunc, was config.Config) {
	t := time.NewTicker(c.poll)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			if now, err := config.Load(); err == nil && (now.HelperToken != was.HelperToken || now.Relay != was.Relay) {
				cancel()
				return
			}
		}
	}
}

func (c *cli) installService() int {
	exe, err := os.Executable()
	if err != nil {
		c.printf(c.err, "Cannot find the ailimits program path.")
		return 1
	}
	// Not EvalSymlinks: a Homebrew Cellar path disappears on upgrade.
	exe = service.StablePath(exe, func(p string) bool { _, e := os.Stat(p); return e == nil })
	// The service reads the same Claude Code profile as this terminal.
	if err := service.Install(exe, serviceConfigDir(os.Getenv("CLAUDE_CONFIG_DIR"))); err != nil {
		c.printf(c.err, "Could not install the service: %v", err)
		return 1
	}
	c.printf(c.out, "Installed. AI Limits now starts when you log in.")
	if cfg, err := config.Load(); err == nil && cfg.HelperToken == "" {
		c.printf(c.out, "Next: ailimits pair <CODE>   (the code is shown on your watch)")
	}
	return 0
}

// serviceConfigDir makes the Claude Code profile path absolute and clean: the service
// starts from another working directory, and a trailing "\" would escape the closing
// quote of the Windows Run value.
func serviceConfigDir(dir string) string {
	if dir == "" {
		return ""
	}
	if abs, err := filepath.Abs(dir); err == nil {
		return abs
	}
	return filepath.Clean(dir)
}

func (c *cli) uninstallService() int {
	if err := service.Uninstall(); err != nil {
		c.printf(c.err, "Could not remove the service: %v", err)
		return 1
	}
	c.printf(c.out, "Removed. AI Limits no longer starts automatically.")
	return 0
}

// formatRows renders one line per limit, e.g. "5-hour session  7%  resets in 3h 2m".
func formatRows(rows []snapshot.Row, now time.Time) []string {
	lines := make([]string, 0, len(rows))
	for _, r := range rows {
		name := r.Label
		switch r.Kind {
		case "session":
			name = "5-hour session"
		case "weekly_all":
			name = "week"
		case "weekly_scoped":
			if name == "" {
				name = "week"
			} else {
				name += " (week)"
			}
		}
		if name == "" {
			name = r.Kind
		}
		line := fmt.Sprintf("%s  %d%%", name, r.Pct)
		if r.Reset > 0 {
			line += "  resets " + untilText(time.Unix(r.Reset, 0).Sub(now))
		}
		lines = append(lines, line)
	}
	return lines
}

func untilText(d time.Duration) string {
	if d < time.Minute {
		return "soon"
	}
	mins := int(d / time.Minute)
	switch {
	case mins >= 24*60:
		return fmt.Sprintf("in %dd %dh", mins/(24*60), mins%(24*60)/60)
	case mins >= 60:
		return fmt.Sprintf("in %dh %dm", mins/60, mins%60)
	}
	return fmt.Sprintf("in %dm", mins)
}
