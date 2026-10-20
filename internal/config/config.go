// Package config loads pr-deck's user configuration.
//
// Every organisation-specific value (GitHub host, Jira site, Slack channel,
// labels, merge windows) comes from the user's config file or environment.
// Nothing here defaults to a real organisation, and tokens are never read
// from the config file itself: only from environment variables or a
// user-supplied token command.
package config

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/BurntSushi/toml"
)

// Config is the full pr-deck configuration.
type Config struct {
	GitHub       GitHub       `toml:"github"`
	Jira         Jira         `toml:"jira"`
	Review       Review       `toml:"review"`
	MergeWindows MergeWindows `toml:"merge_windows"`
	Why          Why          `toml:"why"`
	Herdr        Herdr        `toml:"herdr"`
	Deck         Deck         `toml:"deck"`

	// ProductionLabel marks PRs whose plan changes production (▲).
	ProductionLabel string `toml:"production_label"`
	// ProductionLabelIsCritical makes the production label count as
	// critical for merge windows.
	ProductionLabelIsCritical bool `toml:"production_label_is_critical"`
	// Envs lists environment names in merge order (eng before prod).
	Envs []string `toml:"envs"`

	// path is where the config was loaded from ("" for defaults).
	path string
}

// GitHub configures the PR source.
type GitHub struct {
	APIURL      string   `toml:"api_url"`      // GraphQL/REST base, e.g. https://api.github.com
	WebHost     string   `toml:"web_host"`     // host in PR URLs, e.g. github.com
	Query       string   `toml:"query"`        // extra search qualifiers, e.g. "org:my-org"
	MergeMethod string   `toml:"merge_method"` // squash | merge | rebase
	TokenEnv    []string `toml:"token_env"`    // env vars tried in order
	TokenCmd    []string `toml:"token_cmd"`    // fallback command printing a token
	// IgnoreCommenters are logins whose comments never count as human; *[bot] always is.
	IgnoreCommenters []string `toml:"ignore_commenters"`
}

// Jira configures ticket topics.
type Jira struct {
	BaseURL  string   `toml:"base_url"` // e.g. https://example.atlassian.net
	Email    string   `toml:"email"`
	TokenEnv string   `toml:"token_env"`
	TokenCmd []string `toml:"token_cmd"`
	Projects []string `toml:"projects"`  // optional: only these project keys count
	ShortLen int      `toml:"short_len"` // max runes for the short topic form
}

// Review configures review-channel tracking.
type Review struct {
	Channel     string   `toml:"channel"`      // e.g. #team-reviews; empty disables tracking
	RemindAfter Duration `toml:"remind_after"` // posted with no review this long → overdue
	TokenEnv    string   `toml:"token_env"`
	TokenCmd    []string `toml:"token_cmd"`
	APIURL      string   `toml:"api_url"`
	SkipDrafts  bool     `toml:"skip_drafts"`
}

// MergeWindows configures when critical PRs may merge.
type MergeWindows struct {
	AppliesTo             []string `toml:"applies_to"` // env:<name> | label:<name>
	Days                  []string `toml:"days"`       // mon..sun; empty disables windows
	Slots                 []string `toml:"slots"`      // "09:30-11:40"
	Timezone              string   `toml:"timezone"`   // IANA name or "Local"
	BlockHoliday          bool     `toml:"block_holiday"`
	BlockDayBeforeHoliday bool     `toml:"block_day_before_holiday"`
	HolidaysICS           string   `toml:"holidays_ics"` // URL or file path
	Holidays              []string `toml:"holidays"`     // extra YYYY-MM-DD dates
	OutsideWindow         string   `toml:"outside_window"`
	RemindBefore          Duration `toml:"remind_before"`
	MissedWindow          string   `toml:"missed_window"` // next_window | skip
}

// Why configures where "why does this PR exist" comes from.
type Why struct {
	TranscriptsDir string `toml:"transcripts_dir"`
}

// Herdr configures the herdr host integration.
type Herdr struct {
	SocketPath string   `toml:"socket_path"` // default: $HERDR_SOCKET_PATH
	Source     string   `toml:"source"`      // metadata source id
	Toasts     bool     `toml:"toasts"`
	Position   string   `toml:"position"`
	RecallAt   string   `toml:"recall_at"` // "17:30" daily nudge; empty disables
	Disabled   bool     `toml:"disabled"`
	Notify     []string `toml:"notify_cmd"` // non-herdr toast command; {title} {body}
}

// Deck holds general behaviour.
type Deck struct {
	Refresh   Duration `toml:"refresh"`
	StateDir  string   `toml:"state_dir"`
	CacheDir  string   `toml:"cache_dir"`
	OpenCmd   []string `toml:"open_cmd"`
	CopyCmd   []string `toml:"copy_cmd"`
	StaleWarn Duration `toml:"stale_warn"`
}

// Duration wraps time.Duration for TOML strings like "5h".
type Duration struct{ time.Duration }

// UnmarshalText implements encoding.TextUnmarshaler.
func (d *Duration) UnmarshalText(b []byte) error {
	v, err := time.ParseDuration(strings.TrimSpace(string(b)))
	if err != nil {
		return err
	}
	d.Duration = v
	return nil
}

// MarshalText implements encoding.TextMarshaler.
func (d Duration) MarshalText() ([]byte, error) { return []byte(d.String()), nil }

// String prints "5h" rather than "5h0m0s".
func (d Duration) String() string {
	s := d.Duration.String()
	if strings.HasSuffix(s, "m0s") {
		s = strings.TrimSuffix(s, "0s")
	}
	if strings.HasSuffix(s, "h0m") {
		s = strings.TrimSuffix(s, "0m")
	}
	return s
}

// Default returns a config with neutral defaults: no organisation, no
// channel, no merge windows.
func Default() Config {
	return Config{
		GitHub: GitHub{
			APIURL:           "https://api.github.com",
			WebHost:          "github.com",
			MergeMethod:      "squash",
			TokenEnv:         []string{"GITHUB_TOKEN", "GH_TOKEN"},
			TokenCmd:         []string{"gh", "auth", "token"},
			IgnoreCommenters: []string{"github-actions", "claude-review", "renovate"},
		},
		Jira: Jira{TokenEnv: "JIRA_API_TOKEN", ShortLen: 22},
		Review: Review{
			RemindAfter: Duration{5 * time.Hour},
			TokenEnv:    "SLACK_TOKEN",
			APIURL:      "https://slack.com/api",
			SkipDrafts:  true,
		},
		MergeWindows: MergeWindows{
			Timezone:              "Local",
			BlockHoliday:          true,
			BlockDayBeforeHoliday: true,
			OutsideWindow:         "schedule",
			RemindBefore:          Duration{10 * time.Minute},
			MissedWindow:          "next_window",
		},
		Herdr: Herdr{Source: "custom:pr-deck", Toasts: true, Position: "top-right", RecallAt: "17:30"},
		Deck: Deck{
			Refresh:   Duration{60 * time.Second},
			StaleWarn: Duration{10 * time.Minute},
		},
		ProductionLabelIsCritical: true,
		Envs:                      []string{"eng", "prod"},
	}
}

// DefaultPath is $PRD_CONFIG, else $XDG_CONFIG_HOME/pr-deck/config.toml.
func DefaultPath() string {
	if p := os.Getenv("PRD_CONFIG"); p != "" {
		return p
	}
	base := os.Getenv("XDG_CONFIG_HOME")
	if base == "" {
		home, _ := os.UserHomeDir()
		base = filepath.Join(home, ".config")
	}
	return filepath.Join(base, "pr-deck", "config.toml")
}

// Load reads path over the defaults. A missing file yields the defaults.
func Load(path string) (Config, error) {
	c := Default()
	if path == "" {
		path = DefaultPath()
	}
	b, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return c.finish(), nil
		}
		return c, err
	}
	if _, err := toml.Decode(string(b), &c); err != nil {
		return c, fmt.Errorf("%s: %w", path, err)
	}
	c.path = path
	if err := c.validate(); err != nil {
		return c, fmt.Errorf("%s: %w", path, err)
	}
	return c.finish(), nil
}

// Path is where the config came from, "" when defaults were used.
func (c Config) Path() string { return c.path }

func (c Config) validate() error {
	switch c.GitHub.MergeMethod {
	case "squash", "merge", "rebase":
	default:
		return fmt.Errorf("github.merge_method must be squash, merge or rebase, got %q", c.GitHub.MergeMethod)
	}
	switch c.MergeWindows.OutsideWindow {
	case "schedule", "block":
	default:
		return fmt.Errorf("merge_windows.outside_window must be schedule or block, got %q", c.MergeWindows.OutsideWindow)
	}
	switch c.MergeWindows.MissedWindow {
	case "next_window", "skip":
	default:
		return fmt.Errorf("merge_windows.missed_window must be next_window or skip, got %q", c.MergeWindows.MissedWindow)
	}
	return nil
}

func (c Config) finish() Config {
	home, _ := os.UserHomeDir()
	if c.Why.TranscriptsDir == "" {
		c.Why.TranscriptsDir = filepath.Join(home, ".claude", "projects")
	}
	if c.Deck.StateDir == "" {
		c.Deck.StateDir = stateDir(home)
	}
	if c.Deck.CacheDir == "" {
		c.Deck.CacheDir = cacheDir(home)
	}
	if c.Herdr.SocketPath == "" {
		c.Herdr.SocketPath = os.Getenv("HERDR_SOCKET_PATH")
	}
	if c.Jira.ShortLen <= 0 {
		c.Jira.ShortLen = 22
	}
	c.Why.TranscriptsDir = expand(c.Why.TranscriptsDir, home)
	c.Deck.StateDir = expand(c.Deck.StateDir, home)
	c.Deck.CacheDir = expand(c.Deck.CacheDir, home)
	return c
}

func stateDir(home string) string {
	if p := os.Getenv("HERDR_PLUGIN_STATE_DIR"); p != "" {
		return p
	}
	if p := os.Getenv("XDG_STATE_HOME"); p != "" {
		return filepath.Join(p, "pr-deck")
	}
	return filepath.Join(home, ".local", "state", "pr-deck")
}

func cacheDir(home string) string {
	if p := os.Getenv("XDG_CACHE_HOME"); p != "" {
		return filepath.Join(p, "pr-deck")
	}
	return filepath.Join(home, ".cache", "pr-deck")
}

func expand(p, home string) string {
	if p == "~" {
		return home
	}
	if strings.HasPrefix(p, "~/") {
		return filepath.Join(home, p[2:])
	}
	return p
}

// CriticalSelectors returns merge-window selectors, including the
// production label when it counts as critical.
func (c Config) CriticalSelectors() []string {
	out := append([]string(nil), c.MergeWindows.AppliesTo...)
	if c.ProductionLabel != "" && c.ProductionLabelIsCritical {
		sel := "label:" + c.ProductionLabel
		found := false
		for _, s := range out {
			if s == sel {
				found = true
			}
		}
		if !found {
			out = append(out, sel)
		}
	}
	return out
}

// Secret resolves a token: first non-empty env var, else the command's
// trimmed stdout. Returns "" when neither yields anything.
func Secret(envs []string, cmd []string) string {
	for _, e := range envs {
		if e == "" {
			continue
		}
		if v := strings.TrimSpace(os.Getenv(e)); v != "" {
			return v
		}
	}
	if len(cmd) == 0 {
		return ""
	}
	out, err := exec.Command(cmd[0], cmd[1:]...).Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}
