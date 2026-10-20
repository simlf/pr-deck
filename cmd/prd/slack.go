package main

import (
	"bufio"
	"context"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"regexp"
	"strings"
	"time"

	"github.com/simlf/pr-deck/internal/config"
	"github.com/simlf/pr-deck/internal/sources/slack"
)

const slackSteps = `Slack setup (about 1 minute):
  1. Open https://api.slack.com/apps -> Create New App -> From a manifest.
  2. Pick your workspace and paste docs/slack-app-manifest.yml from the pr-deck repo
     (https://github.com/simlf/pr-deck/blob/main/docs/slack-app-manifest.yml).
  3. Install to Workspace, then copy the "User OAuth Token" (xoxp-...) from OAuth & Permissions.
`

var (
	stdin        io.Reader = os.Stdin
	slackTokenRe           = regexp.MustCompile(`^xox[a-z]-[A-Za-z0-9-]+$`)
	// keychainSave stores the token without putting it on a command line.
	keychainSave = func(token string) error {
		cmd := exec.Command("security", "-i")
		cmd.Stdin = strings.NewReader(fmt.Sprintf("add-generic-password -U -s %s -a %s -w %s\n", config.KeychainService, config.KeychainAccount, token))
		if out, err := cmd.CombinedOutput(); err != nil {
			return fmt.Errorf("keychain: %v: %s", err, strings.TrimSpace(string(out)))
		}
		return nil
	}
)

func runSlack(cfg config.Config, args []string, stdout io.Writer) error {
	if len(args) == 0 || args[0] != "login" {
		return fmt.Errorf("usage: prd slack login [--token]")
	}
	fs := flag.NewFlagSet("slack login", flag.ContinueOnError)
	quiet := fs.Bool("token", false, "skip the setup steps and read the token from stdin")
	if err := fs.Parse(args[1:]); err != nil {
		return err
	}
	if !*quiet {
		fmt.Fprint(stdout, slackSteps+"\nPaste the token: ")
	}
	line, _ := bufio.NewReader(stdin).ReadString('\n')
	tok := strings.TrimSpace(line)
	if !slackTokenRe.MatchString(tok) {
		return fmt.Errorf("that is not a Slack token (xoxp-...)")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	c := &slack.Client{HTTP: &http.Client{Timeout: 20 * time.Second}, APIURL: cfg.Review.APIURL, Token: tok}
	user, team, scopes, err := c.AuthTest(ctx)
	if err != nil {
		return err
	}
	for _, s := range []string{"search:read", "chat:write"} {
		if !strings.Contains(","+strings.ReplaceAll(scopes, " ", "")+",", ","+s+",") {
			return fmt.Errorf("the token lacks the %s user scope: create the app from docs/slack-app-manifest.yml", s)
		}
	}
	if err := keychainSave(tok); err != nil {
		return err
	}
	fmt.Fprintf(stdout, "\nSaved the token of %s (%s) in the macOS Keychain. %s still wins when set.\n", user, team, cfg.Review.TokenEnv)
	return nil
}
