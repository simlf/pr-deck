// Package pairs links PRs that are "the same change, merged in order", such
// as an eng PR that must merge before its prod twin.
//
// Links come from three sources, strongest first:
//   - manual: the user paired them in pr-deck (p key); fixes mistakes.
//   - trailer: the later PR's body says "Merge-after: #N".
//   - heuristic: same ticket, same branch stem, different env suffix.
package pairs

import (
	"regexp"
	"sort"
	"strings"

	"prdeck/internal/model"
	"prdeck/internal/trailer"
)

// DetectEnv finds which configured env a PR targets, from a label
// "env:<name>", a branch suffix like "-prod", or a title tag "(prod)".
func DetectEnv(envs []string, branch, title string, labels []string) string {
	for _, l := range labels {
		for _, e := range envs {
			if strings.EqualFold(l, "env:"+e) {
				return e
			}
		}
	}
	lb := strings.ToLower(branch)
	for _, e := range envs {
		le := strings.ToLower(e)
		for _, sep := range []string{"-", "_", "/", "."} {
			if strings.HasSuffix(lb, sep+le) || strings.Contains(lb, sep+le+sep) {
				return e
			}
		}
	}
	lt := strings.ToLower(title)
	for _, e := range envs {
		le := strings.ToLower(e)
		if strings.Contains(lt, "("+le+")") || strings.Contains(lt, "["+le+"]") {
			return e
		}
	}
	return ""
}

// Stem strips a trailing env marker from a branch name so twins compare
// equal: "abc-412-body-size-eng" and "abc-412-body-size-prod" share a stem.
func Stem(envs []string, branch string) string {
	lb := strings.ToLower(branch)
	for _, e := range envs {
		re := regexp.MustCompile(`[-_/.]` + regexp.QuoteMeta(strings.ToLower(e)) + `([-_/.]|$)`)
		lb = re.ReplaceAllString(lb, "$1")
	}
	return strings.Trim(lb, "-_/.")
}

type edge struct {
	after  string
	source string
}

// Link sets Pair on every PR that belongs to a chain. manual maps a PR key to
// the key that must merge before it.
func Link(prs []model.PR, envs []string, manual map[string]string) {
	byKey := map[string]int{}
	for i, p := range prs {
		byKey[p.Key()] = i
		prs[i].Pair = nil
	}
	edges := map[string]edge{}
	for _, p := range prs {
		if repo, n, ok := trailer.MergeAfter(p.Body); ok {
			if repo == "" {
				repo = p.Repo
			}
			edges[p.Key()] = edge{model.Key(repo, n), "trailer"}
		}
	}
	for k, after := range manual {
		if after == "" {
			delete(edges, k)
			continue
		}
		edges[k] = edge{after, "manual"}
	}
	// Heuristic only for PRs not already in a declared chain.
	inChain := map[string]bool{}
	for k, e := range edges {
		inChain[k] = true
		inChain[e.after] = true
	}
	envIdx := map[string]int{}
	for i, e := range envs {
		envIdx[e] = i
	}
	groups := map[string][]int{}
	for i, p := range prs {
		if inChain[p.Key()] || p.Ticket == "" || p.Env == "" {
			continue
		}
		stem := Stem(envs, p.Branch)
		if stem == "" {
			continue
		}
		g := p.Ticket + "|" + p.Repo + "|" + stem
		groups[g] = append(groups[g], i)
	}
	for _, idx := range groups {
		if len(idx) < 2 {
			continue
		}
		sort.Slice(idx, func(a, b int) bool { return envIdx[prs[idx[a]].Env] < envIdx[prs[idx[b]].Env] })
		distinct := map[string]bool{}
		for _, i := range idx {
			distinct[prs[i].Env] = true
		}
		if len(distinct) != len(idx) {
			continue // two PRs for the same env: too ambiguous to guess
		}
		for j := 1; j < len(idx); j++ {
			edges[prs[idx[j]].Key()] = edge{prs[idx[j-1]].Key(), "heuristic"}
		}
	}
	// Resolve chains: order = 1 + order(after); id = root key.
	var orderOf func(k string, seen map[string]bool) (int, string)
	orderOf = func(k string, seen map[string]bool) (int, string) {
		e, ok := edges[k]
		if !ok || seen[k] {
			return 1, k
		}
		seen[k] = true
		o, root := orderOf(e.after, seen)
		return o + 1, root
	}
	involved := map[string]bool{}
	for k, e := range edges {
		involved[k] = true
		involved[e.after] = true
	}
	inferred := map[string]bool{} // root → all edges heuristic
	rootOf := map[string]string{}
	for k := range involved {
		_, root := orderOf(k, map[string]bool{})
		rootOf[k] = root
	}
	for root := range valuesSet(rootOf) {
		all := true
		for k, e := range edges {
			if rootOf[k] == root && e.source != "heuristic" {
				all = false
			}
		}
		inferred[root] = all
	}
	for k := range involved {
		i, ok := byKey[k]
		if !ok {
			continue
		}
		o, root := orderOf(k, map[string]bool{})
		ref := &model.PairRef{ID: root, Order: o, Inferred: inferred[root]}
		if e, ok := edges[k]; ok {
			ref.After = e.after
			ref.Source = e.source
		} else {
			ref.Source = firstSource(edges, k)
		}
		prs[i].Pair = ref
	}
}

func valuesSet(m map[string]string) map[string]bool {
	out := map[string]bool{}
	for _, v := range m {
		out[v] = true
	}
	return out
}

func firstSource(edges map[string]edge, k string) string {
	for _, e := range edges {
		if e.after == k {
			return e.source
		}
	}
	return ""
}

// ReferencedMissing returns keys named by Merge-after trailers or manual
// pairs that are not in prs, so the caller can fetch merged siblings.
func ReferencedMissing(prs []model.PR, manual map[string]string) []string {
	have := map[string]bool{}
	for _, p := range prs {
		have[p.Key()] = true
	}
	var out []string
	add := func(k string) {
		if k != "" && !have[k] {
			have[k] = true
			out = append(out, k)
		}
	}
	for _, p := range prs {
		if repo, n, ok := trailer.MergeAfter(p.Body); ok {
			if repo == "" {
				repo = p.Repo
			}
			add(model.Key(repo, n))
		}
	}
	for k, after := range manual {
		if have[k] {
			add(after)
		}
	}
	sort.Strings(out)
	return out
}
