// Command prd is pr-deck: see your open PRs grouped by topic, eng → prod
// merge order, review state and "why does this PR exist", from a terminal
// pane, a herdr popup or a tmux status line.
package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"regexp"
	"strings"
	"syscall"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"prdeck/internal/config"
	"prdeck/internal/daemon"
	"prdeck/internal/deck"
	"prdeck/internal/herdr"
	"prdeck/internal/pairs"
	"prdeck/internal/strip"
	"prdeck/internal/tui"
)

var version = "dev"

const usage = `prd: pr-deck, your open PRs by topic, merge order and why

usage:
  prd [board]                 board layout (topics, pairs, review, next action)
  prd recall                  end-of-day recall layout
  prd open [--layout L] [--url URL | --from-link]
                              open the TUI on one PR (herdr link handler)
  prd strip [--line 1|2] [--format plain|tmux|ansi] [--cwd DIR]
                              status-line output from the cache (no network)
  prd daemon [--once|--detach] refresh, scheduled merges, reminders, herdr tokens
  prd poke                    ask the running daemon to refresh soon
  prd herdr-open <pane> [--focus-from-context]
                              open a pr-deck pane in herdr (used by plugin actions)
  prd refresh                 refresh the cache once and print a summary
  prd doctor                  check configuration and credentials (never prints tokens)
  prd version

config: $PRD_CONFIG or ~/.config/pr-deck/config.toml (see docs/config.example.toml)
`

func main() {
	if err := run(os.Args[1:], os.Stdout, os.Stderr); err != nil {
		fmt.Fprintln(os.Stderr, "prd:", err)
		os.Exit(1)
	}
}

func run(args []string, stdout, stderr io.Writer) error {
	cmd := "board"
	if len(args) > 0 && !strings.HasPrefix(args[0], "-") {
		cmd, args = args[0], args[1:]
	}
	switch cmd {
	case "help", "-h", "--help":
		fmt.Fprint(stdout, usage)
		return nil
	case "version":
		fmt.Fprintln(stdout, "prd", version)
		return nil
	}
	cfg, err := config.Load("")
	if err != nil {
		return err
	}
	switch cmd {
	case "board", "recall":
		return runTUI(cfg, tui.Options{Layout: cmd})
	case "open":
		fs := flag.NewFlagSet("open", flag.ContinueOnError)
		layout := fs.String("layout", "board", "board or recall")
		url := fs.String("url", "", "PR URL or owner/repo#N to focus")
		fromLink := fs.Bool("from-link", false, "read the clicked URL from HERDR_PLUGIN_CONTEXT_JSON")
		if err := fs.Parse(args); err != nil {
			return err
		}
		focus := *url
		if focus == "" {
			focus = os.Getenv("PRD_FOCUS")
		}
		if *fromLink && focus == "" {
			focus = clickedURL(os.Getenv("HERDR_PLUGIN_CONTEXT_JSON"))
		}
		return runTUI(cfg, tui.Options{Layout: *layout, Focus: strings.TrimSpace(focus)})
	case "strip":
		fs := flag.NewFlagSet("strip", flag.ContinueOnError)
		ln := fs.Int("line", 0, "1, 2, or 0 for both")
		format := fs.String("format", strip.Plain, "plain, tmux or ansi")
		cwd := fs.String("cwd", "", "directory whose checked-out branch is 'this session'")
		if err := fs.Parse(args); err != nil {
			return err
		}
		return runStrip(cfg, *ln, *format, *cwd, stdout)
	case "daemon":
		fs := flag.NewFlagSet("daemon", flag.ContinueOnError)
		once := fs.Bool("once", false, "run a single tick and exit")
		detach := fs.Bool("detach", false, "start in the background (logs to the state dir) and return")
		if err := fs.Parse(args); err != nil {
			return err
		}
		if *detach {
			return detachDaemon(cfg, stdout)
		}
		return runDaemon(cfg, *once, stderr)
	case "poke":
		return daemon.Poke(cfg.Deck.CacheDir)
	case "herdr-open":
		fs := flag.NewFlagSet("herdr-open", flag.ContinueOnError)
		fromCtx := fs.Bool("focus-from-context", false, "focus the PR URL herdr passed (link handler / selection)")
		if err := fs.Parse(args); err != nil {
			return err
		}
		pane := "board"
		if fs.NArg() > 0 {
			pane = fs.Arg(0)
		}
		env := map[string]string{}
		if *fromCtx {
			if u := clickedURL(os.Getenv("HERDR_PLUGIN_CONTEXT_JSON")); u != "" {
				env["PRD_FOCUS"] = u
			}
		}
		id := os.Getenv("HERDR_PLUGIN_ID")
		if id == "" {
			id = "pr-deck"
		}
		return (&herdr.Client{Socket: cfg.Herdr.SocketPath}).OpenPane(id, pane, env)
	case "refresh":
		return runRefresh(cfg, stdout)
	case "doctor":
		return runDoctor(cfg, stdout)
	}
	fmt.Fprint(stderr, usage)
	return fmt.Errorf("unknown command %q", cmd)
}

func runTUI(cfg config.Config, opt tui.Options) error {
	m := tui.New(deck.New(cfg), opt)
	p := tea.NewProgram(m, tea.WithAltScreen(), tea.WithMouseCellMotion())
	_, err := p.Run()
	return err
}

// clickedURL pulls the PR URL out of herdr's invocation context.
func clickedURL(ctxJSON string) string {
	if ctxJSON == "" {
		return ""
	}
	var c struct {
		ClickedURL   string `json:"clicked_url"`
		SelectedText string `json:"selected_text"`
	}
	if json.Unmarshal([]byte(ctxJSON), &c) == nil {
		if c.ClickedURL != "" {
			return c.ClickedURL
		}
		if m := prURL.FindString(c.SelectedText); m != "" {
			return m
		}
	}
	return prURL.FindString(ctxJSON)
}

var prURL = regexp.MustCompile(`https?://[^\s"]+/pull/\d+`)

func runStrip(cfg config.Config, ln int, format, cwd string, w io.Writer) error {
	d := deck.New(cfg)
	snap, ok := d.Cached()
	if !ok {
		fmt.Fprintln(w, "pr-deck: no data yet (run prd daemon)")
		return nil
	}
	st, _ := d.Store.Load()
	rules, err := d.RulesCached()
	if err != nil {
		return err
	}
	pairs.Link(snap.PRs, cfg.Envs, st.Pairs) // reflect pairs set since the last refresh
	v := deck.View{Snap: snap, State: st, Rules: rules, Cfg: cfg, Now: time.Now()}
	if ln == 0 || ln == 1 {
		fmt.Fprintln(w, strip.Line1(v, format))
	}
	if ln == 0 || ln == 2 {
		fmt.Fprintln(w, strip.Line2(v, cwd, format))
	}
	return nil
}

func detachDaemon(cfg config.Config, w io.Writer) error {
	lock, ok, err := daemon.Lock(cfg.Deck.CacheDir)
	if err != nil {
		return err
	}
	if !ok {
		fmt.Fprintln(w, "prd daemon already running")
		return nil
	}
	lock.Close() // the child takes it again
	if err := os.MkdirAll(cfg.Deck.StateDir, 0o700); err != nil {
		return err
	}
	logf, err := os.OpenFile(filepath.Join(cfg.Deck.StateDir, "daemon.log"), os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		return err
	}
	defer logf.Close()
	self, err := os.Executable()
	if err != nil {
		return err
	}
	c := exec.Command(self, "daemon")
	c.Stdout, c.Stderr = logf, logf
	c.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	if err := c.Start(); err != nil {
		return err
	}
	fmt.Fprintln(w, "prd daemon started, pid", c.Process.Pid, "log", logf.Name())
	return c.Process.Release()
}

func runDaemon(cfg config.Config, once bool, logw io.Writer) error {
	if !once {
		lock, ok, err := daemon.Lock(cfg.Deck.CacheDir)
		if err != nil {
			return err
		}
		if !ok {
			fmt.Fprintln(logw, "prd daemon already running")
			return nil
		}
		defer lock.Close()
	}
	d := &daemon.Daemon{Deck: deck.New(cfg), Log: logw}
	if cfg.Herdr.SocketPath != "" && !cfg.Herdr.Disabled {
		c := &herdr.Client{Socket: cfg.Herdr.SocketPath}
		d.Host = c
		if cfg.Herdr.Toasts {
			d.Notify = daemon.HerdrNotifier{Client: c, Position: cfg.Herdr.Position}
		}
	}
	if d.Notify == nil && len(cfg.Herdr.Notify) > 0 {
		d.Notify = daemon.CmdNotifier{Cmd: cfg.Herdr.Notify}
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	if once {
		return d.Tick(ctx)
	}
	err := d.Run(ctx)
	if ctx.Err() != nil {
		return nil
	}
	return err
}

func runRefresh(cfg config.Config, w io.Writer) error {
	d := deck.New(cfg)
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	snap, err := d.Refresh(ctx)
	if err != nil {
		return err
	}
	st, _ := d.Store.Load()
	rules, err := d.Rules(ctx)
	if err != nil {
		return err
	}
	v := deck.View{Snap: snap, State: st, Rules: rules, Cfg: cfg, Now: time.Now()}
	fmt.Fprintln(w, strip.Line1(v, strip.Plain))
	for _, p := range snap.PRs {
		if p.Sibling {
			continue
		}
		s := v.StateOf(p)
		fmt.Fprintf(w, "  %s %-28s %-24s %s\n", deck.Glyph(s), p.Ref(), v.TopicShort(p), v.Next(p))
	}
	for _, e := range snap.Errors {
		fmt.Fprintln(w, "  warning:", e)
	}
	return nil
}

func runDoctor(cfg config.Config, w io.Writer) error {
	yes := func(b bool) string {
		if b {
			return "ok"
		}
		return "missing"
	}
	path := cfg.Path()
	if path == "" {
		path = config.DefaultPath() + " (not found, using defaults)"
	}
	fmt.Fprintln(w, "config:      ", path)
	fmt.Fprintln(w, "github:      ", cfg.GitHub.APIURL, "token", yes(config.Secret(cfg.GitHub.TokenEnv, cfg.GitHub.TokenCmd) != ""))
	if cfg.Jira.BaseURL == "" {
		fmt.Fprintln(w, "jira:         not configured (topics fall back to Purpose: lines and titles)")
	} else {
		fmt.Fprintln(w, "jira:        ", cfg.Jira.BaseURL, "token", yes(config.Secret([]string{cfg.Jira.TokenEnv}, cfg.Jira.TokenCmd) != ""))
	}
	if cfg.Review.Channel == "" {
		fmt.Fprintln(w, "review:       no channel (posted/overdue tracking off)")
	} else {
		fmt.Fprintln(w, "review:      ", cfg.Review.Channel, "remind after", cfg.Review.RemindAfter, "slack token", yes(config.Secret([]string{cfg.Review.TokenEnv}, cfg.Review.TokenCmd) != ""))
	}
	d := deck.New(cfg)
	rules, err := d.RulesCached()
	switch {
	case err != nil:
		fmt.Fprintln(w, "windows:      error:", err)
	case !rules.Enabled():
		fmt.Fprintln(w, "windows:      none (critical PRs merge any time)")
	default:
		fmt.Fprintln(w, "windows:     ", strings.Join(cfg.MergeWindows.Days, ","), strings.Join(cfg.MergeWindows.Slots, ","),
			"applies to", strings.Join(cfg.CriticalSelectors(), ","), "outside:", cfg.MergeWindows.OutsideWindow)
		fmt.Fprintln(w, "             ", rules.At(time.Now()).Describe(time.Now()))
	}
	_, statErr := os.Stat(cfg.Why.TranscriptsDir)
	fmt.Fprintln(w, "transcripts: ", cfg.Why.TranscriptsDir, yes(statErr == nil))
	if cfg.Herdr.SocketPath == "" {
		fmt.Fprintln(w, "herdr:        not detected (HERDR_SOCKET_PATH unset)")
	} else {
		_, err := (&herdr.Client{Socket: cfg.Herdr.SocketPath, Timeout: 2 * time.Second}).Agents()
		status := "reachable"
		if err != nil {
			status = "unreachable: " + err.Error()
		}
		fmt.Fprintln(w, "herdr:       ", cfg.Herdr.SocketPath, status)
	}
	fmt.Fprintln(w, "state:       ", cfg.Deck.StateDir)
	fmt.Fprintln(w, "cache:       ", cfg.Deck.CacheDir)
	if snap, ok := d.Cached(); ok {
		fmt.Fprintf(w, "snapshot:     %d PRs, %s old\n", len(snap.PRs), time.Since(snap.At).Round(time.Second))
	} else {
		fmt.Fprintln(w, "snapshot:     none yet (prd refresh or prd daemon)")
	}
	return nil
}
