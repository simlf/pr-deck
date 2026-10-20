package tui

// Guide is the keys, marks and typical flows: the ? overlay and `prd tutorial` print it.
const Guide = `pr-deck guide

KEYS
  j/k ↑/↓       move · g/G top/bottom · wheel scrolls
  enter / o     open the PR in the browser (double-click a row too)
  tab, enter    pick a terraform plan in the drawer (lists its changed
                resources), then open its comment
  c             open the newest human comment (💬); this clears the mark
  v             read the PR diff (a new herdr tab under herdr); there,
                c on a line sends a file:line note to firstmate
  J             open the Jira ticket (click the grey key in a group header too)
  w             why: purpose, the prompt that created the PR, pair, review
  y / Y         copy the link / copy a summary for chat
  P             post "topic (KEY): title url" to the review channel (asks first);
                once posted, copy a nudge; without a Slack token, copy the request
  r             rerun failed CI jobs
  m             merge with every guard; outside a prod window, schedule it;
                on a scheduled PR, cancel the schedule
  p / u         pair: pick the PR that must merge first / unpair
  f             reviewed: clear ⚑ until tomorrow
  t             rename the topic's short form
  R / s         switch board ↔ recall / cycle the recall sort
  h / l         fold / unfold topic groups (click a header too)
  /             filter by topic, repo, ticket or title
  ctrl+r        refresh now
  ? / q         this guide / quit

MARKS
  state    ● ready  ○ needs review  ✗ CI failed or conflict  ◌ CI running
           ◇ waits on its sibling  ◦ draft  ◷ merge scheduled  ✓ merged
  review   ✓ approved  ✎ changes requested  ◌ posted  ◔ overdue  ○ not posted
  other    Δ N ws  terraform plans with changes (red: a plan destroys)
           💬 N  new human comments since your last push or action
           ⚑ needs you and untouched today  ▲ production label
           ┌① └②  merge order of an eng → prod chain (┆ inferred, p confirms)

FLOWS
  Ask for a review   select the PR, press P, then y. pr-deck posts once;
                     press P again later to copy a nudge.
  Merge in order     select ①, press m, then y. Then select ②: m merges it,
                     or offers a schedule when the prod window is closed.
  Fix a red CI       press r. The PR shows ⟳ CI rerun until the new runs start.
  Read a plan        press tab to pick a workspace and list its resources
                     (+ create, ~ update, -/+ replace, - destroy); enter opens it.
  Answer a comment   💬 on a PR: press c to open the newest comment.
  End of day         press R for recall, s to sort, f on each PR you checked.

MORE
  prd doctor     check the config and the tokens
  prd tutorial   print this guide
  README         https://github.com/simlf/pr-deck#readme
`
