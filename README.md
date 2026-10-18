# pr-deck

Terminal overlay to see work in progress: open PRs grouped by the topic (Jira ticket) they serve, merge order across environments, review state, and a quick "why does this PR exist" recall.

`prd` shows your open GitHub PRs:

- **Grouped by topic, not by key.** A group reads "Allow bigger uploads (ingress 25 MB)", with `ABC-412` small and grey after it. Topics come from the Jira summary, and `t` renames the short form used in tight spots.
- **Eng → prod chains in merge order.** `┌①` and `└②` connect the two halves of a pair, and the prod half shows `◇ waits ①` until eng merges.
- **Review state on every PR:** ✓ approved, ✎ changes requested, ◌ posted, ◔ overdue (posted in the review channel and still unreviewed after 5h), ○ not posted.
- **Why the PR exists:** the `Purpose:` line from the PR body plus the exact prompt you gave the agent that opened it, read from Claude Code transcripts.
- **⚑ untouched today:** PRs that need you and that you have not touched since midnight. PRs waiting on a sibling, on CI or on a scheduled merge are skipped.

It also does a few light actions, all with a confirmation:

- open in the browser
- copy the link or a chat summary
- copy a review request
- rerun failed CI
- merge in the right order, inside prod merge windows, or **scheduled** for the next window

## Install

```sh
go install ./cmd/prd          # Go 1.24+
prd doctor                    # shows what is configured; never prints tokens
```

Copy `docs/config.example.toml` to `~/.config/pr-deck/config.toml` (or set `PRD_CONFIG`) and replace the example values. With no config at all, pr-deck still works with GitHub alone, using `GITHUB_TOKEN`, `GH_TOKEN` or `gh auth token`.

Tokens are never read from the config file:

| Source | Token from | Scope |
|---|---|---|
| GitHub | `token_env` (default `GITHUB_TOKEN`, `GH_TOKEN`) or `token_cmd` (default `gh auth token`) | repo read, merge, actions rerun |
| Jira | `jira.token_env` (default `JIRA_API_TOKEN`) or `jira.token_cmd` | read issues |
| Slack | `review.token_env` (default `SLACK_TOKEN`) or `review.token_cmd` | user token with `search:read`. pr-deck only searches and never posts |

## Hosts

The same `prd` binary runs everywhere. A background daemon keeps one cache, which every view reads:

```sh
prd daemon --detach    # refresh, scheduled merges, reminders, herdr tokens (single instance)
```

### herdr (primary)

`herdr-plugin.toml` at the repo root is a herdr plugin:

- A startup hook starts the daemon.
- An agent-status event hook (`prd poke`) refreshes soon after an agent goes idle, which is usually just after `gh pr create`.
- Popup panes `board` and `recall` hold the TUI, and actions `pr-deck: board`, `pr-deck: end-of-day recall` and `pr-deck: why this PR` open them. Bind an action to a key in herdr's config with `type = "plugin_action"`, for example `ctrl+b p` for the board.
- A **link handler** catches a ctrl/cmd-click on any PR URL in a pane (with `ui.mouse_capture` on) and opens that PR's why in pr-deck instead of the browser.
- **Sidebar tokens:** each agent's row gets a `pr` token listing the PRs it opened (`①#318● ②#319◇`), matched by herdr's agent session id or working directory. Workspaces get `prdeck` counts, `prwin` merge-window state and `prsched` scheduled merges. Tokens have a TTL, so they disappear if the daemon stops.
- **Toasts** (`notification.show`), each sent once:
  - a prod PR is unblocked because its eng sibling merged
  - a review is overdue
  - the prod window opens
  - 10 minutes before a scheduled merge, and its result
  - the daily recall nudge (`herdr.recall_at`, default 17:30)

### tmux (fallback)

```tmux
set -g status 3                     # keep tmux's own line, add two pr-deck lines
set -g status-interval 15
set -g status-format[1] '#(prd strip --line 1 --format tmux)'
set -g status-format[2] '#(prd strip --line 2 --format tmux --cwd "#{pane_current_path}")'
bind-key p display-popup -E -w 90% -h 85% prd board
bind-key R display-popup -E -w 95% -h 90% prd recall
```

`prd strip` reads only the cache, so it is cheap to run from a status line:

- Line 1 shows counts, the most relevant eng → prod chain and the merge window.
- Line 2 shows "this session": the PR for the branch checked out in that pane.

### Plain terminal

`prd` (board) or `prd recall`. Without herdr, toasts can go through `herdr.notify_cmd`, for example `terminal-notifier` or `notify-send`.

## Keys (mouse works too)

| Key | Action |
|---|---|
| `j`/`k`, `↑`/`↓`, wheel | move (click a row to select it) |
| `enter`/`o`, double-click | open the PR in the browser |
| `J`, click the grey key | open the Jira ticket |
| `w` | full why: purpose, the prompt that created the PR, pair, review |
| `y` / `Y` | copy link / copy "topic (KEY) · PR · purpose" |
| `P` | copy a review request (or a nudge) for your review channel |
| `r` | rerun failed CI jobs |
| `m` | merge, with every guard below; on a scheduled PR, cancel the schedule |
| `p` | pair: pick the PR that must merge first (`u` unpairs) |
| `f` | reviewed: clear ⚑ until tomorrow |
| `t` | rename the topic's short form |
| `R` / `s` | switch board ↔ recall / cycle the recall sort (needs you, age, repo) |
| `h`/`l`, click a header | fold / unfold topic groups |
| `/` | filter by topic, repo, ticket or title |
| `ctrl+r`, `?`, `q` | refresh now, help, quit |

### Merge guards

`m` runs these in order and always asks `y`/`n` before acting:

1. **Pair order.** If the earlier sibling is not merged, pr-deck offers to merge it first.
2. **CI, draft, conflicts, review.** A PR without approval is refused with the reason, for example "posted in #team-reviews 6d ago, still no review".
3. **Merge windows** (`[merge_windows]`) for *critical* PRs: `applies_to` env/label selectors, plus the production label when `production_label_is_critical`. Windows are configurable weekdays and slots, the holidays from a team `.ics` feed, and the day before each holiday. Outside a window, `m` offers to **schedule** the merge for the next opening.

A scheduled merge (◷) behaves as follows:

- It is stored in pr-deck's local state, not as GitHub auto-merge, because auto-merge would ignore the window.
- A toast arrives 10 minutes before it fires, so you can cancel it.
- At fire time it re-checks CI, approval, pair order, the window and mergeability on fresh data. If any check fails it skips the merge, keeps the schedule and says why once.
- The prod half of a pair can only be scheduled once its eng sibling has merged.
- If the machine was asleep at the opening, it catches up on wake while the window is still open, and otherwise rolls over to the next window with a toast.

## Conventions for agents that open PRs

pr-deck works without them, but two body lines make it precise. Ask your agent to write them when it runs `gh pr create`:

```text
Purpose: one plain-language sentence saying why this PR exists
Merge-after: #318              # this PR merges after #318 in the same repo
Merge-after: owner/repo#318    # ...or after a PR in another repo
```

- `Purpose:` is the one-liner shown everywhere, so pr-deck makes no LLM calls.
- `Merge-after:` declares an eng → prod (or any ordered) chain. Without it, pr-deck infers pairs from the same ticket and the same branch stem with a different env suffix (`abc-412-body-size-eng` and `-prod`). It draws inferred pairs with a dashed `┆` until you confirm them with `p`.
- Put the env in the branch suffix (`-eng`, `-prod`), a title tag (`(prod)`) or an `env:prod` label.

## Where the data comes from

- **GitHub GraphQL:** `author:@me is:open` plus `github.query`. It provides CI rollup, review decision, reviewers, labels, mergeability and last commit. Merge and rerun use the same API.
- **Jira REST:** the summary and status for each ticket key found in the branch, title or body.
- **Slack `search.messages`:** the first post of each PR URL in `review.channel`.
- **Claude Code transcripts** (`~/.claude/projects/*/*.jsonl`): the user prompt before the `gh pr create` that printed the PR URL. The index is incremental and cached.
- **Team `.ics` feed:** holidays, fetched at most daily and cached.

Local files:

- state: `$XDG_STATE_HOME/pr-deck`, or herdr's plugin state dir. It holds schedules, reviewed marks, aliases, manual pairs and sent reminders.
- cache: `$XDG_CACHE_HOME/pr-deck`. It holds the snapshot, the why index and holidays.

## Development

```sh
go test ./...
```

Tests use fixtures and fakes (httptest servers, a fake herdr socket) and never call GitHub, Jira or Slack.
