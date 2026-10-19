// Package transcript answers "why does this PR exist?" from Claude Code
// session transcripts: it finds the `gh pr create` call that printed a PR's
// URL and keeps the user prompt that came before it, verbatim.
package transcript

import (
	"bufio"
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/simlf/pr-deck/internal/model"
)

// Index maps PR keys (owner/repo#N) to their creation context. It is
// incremental: files whose size and mtime are unchanged are skipped.
type Index struct {
	Files map[string]fileMark  `json:"files"`
	Whys  map[string]model.Why `json:"whys"`
}

// fileMark remembers how far a transcript was read and the context needed
// to resume there, so a growing session file is only read once.
type fileMark struct {
	Size       int64     `json:"size"`
	ModTime    time.Time `json:"mod_time"`
	Offset     int64     `json:"offset"`
	LastPrompt string    `json:"last_prompt,omitempty"`
	Creates    []string  `json:"creates,omitempty"`
}

// Load reads a persisted index (missing file → empty).
func Load(path string) *Index {
	ix := &Index{Files: map[string]fileMark{}, Whys: map[string]model.Why{}}
	b, err := os.ReadFile(path)
	if err != nil {
		return ix
	}
	_ = json.Unmarshal(b, ix)
	if ix.Files == nil {
		ix.Files = map[string]fileMark{}
	}
	if ix.Whys == nil {
		ix.Whys = map[string]model.Why{}
	}
	return ix
}

// Save writes the index atomically.
func (ix *Index) Save(path string) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	b, err := json.Marshal(ix)
	if err != nil {
		return err
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, b, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

// Scan walks dir/*/*.jsonl and indexes new or changed files. webHost limits
// matches to PR URLs on that host (e.g. github.com).
func (ix *Index) Scan(dir, webHost string) error {
	files, err := filepath.Glob(filepath.Join(dir, "*", "*.jsonl"))
	if err != nil {
		return err
	}
	re := prURLRe(webHost)
	for _, f := range files {
		st, err := os.Stat(f)
		if err != nil {
			continue
		}
		old, seen := ix.Files[f]
		if seen && old.Size == st.Size() && old.ModTime.Equal(st.ModTime()) {
			continue
		}
		if !seen || st.Size() < old.Offset {
			old = fileMark{} // new or truncated/rewritten: start over
		}
		mark, err := ix.scanFile(f, re, old)
		if err != nil {
			continue
		}
		mark.Size, mark.ModTime = st.Size(), st.ModTime()
		ix.Files[f] = mark
	}
	return nil
}

// Lookup returns the why for key.
func (ix *Index) Lookup(key string) (model.Why, bool) {
	w, ok := ix.Whys[key]
	return w, ok
}

func prURLRe(host string) *regexp.Regexp {
	if host == "" {
		host = "github.com"
	}
	return regexp.MustCompile(`https?://` + regexp.QuoteMeta(host) + `/([A-Za-z0-9_.-]+)/([A-Za-z0-9_.-]+)/pull/(\d+)`)
}

type line struct {
	Type      string    `json:"type"`
	IsMeta    bool      `json:"isMeta"`
	Cwd       string    `json:"cwd"`
	SessionID string    `json:"sessionId"`
	Timestamp time.Time `json:"timestamp"`
	Message   struct {
		Role    string          `json:"role"`
		Content json.RawMessage `json:"content"`
	} `json:"message"`
}

type block struct {
	Type      string          `json:"type"`
	Text      string          `json:"text"`
	ID        string          `json:"id"`
	Name      string          `json:"name"`
	Input     json.RawMessage `json:"input"`
	ToolUseID string          `json:"tool_use_id"`
	Content   json.RawMessage `json:"content"`
}

func (ix *Index) scanFile(path string, re *regexp.Regexp, from fileMark) (fileMark, error) {
	f, err := os.Open(path)
	if err != nil {
		return from, err
	}
	defer f.Close()
	if _, err := f.Seek(from.Offset, 0); err != nil {
		return from, err
	}
	rd := bufio.NewReaderSize(f, 256*1024)
	lastPrompt := from.LastPrompt
	creates := map[string]bool{} // tool_use ids that ran a PR create
	for _, id := range from.Creates {
		creates[id] = true
	}
	offset := from.Offset
	for {
		raw, err := rd.ReadBytes('\n')
		if err != nil {
			break // EOF or a partial last line: resume there next time
		}
		offset += int64(len(raw))
		var ln line
		if json.Unmarshal(raw, &ln) != nil || ln.IsMeta {
			continue
		}
		var text string
		var blocks []block
		if json.Unmarshal(ln.Message.Content, &text) != nil {
			_ = json.Unmarshal(ln.Message.Content, &blocks)
		}
		switch ln.Type {
		case "user":
			if p := promptText(text, blocks); p != "" {
				lastPrompt = p
			}
			for _, b := range blocks {
				if b.Type != "tool_result" || !creates[b.ToolUseID] {
					continue
				}
				delete(creates, b.ToolUseID)
				for _, m := range re.FindAllStringSubmatch(resultText(b.Content), -1) {
					n, _ := strconv.Atoi(m[3])
					key := model.Key(m[1]+"/"+m[2], n)
					if _, seen := ix.Whys[key]; seen || lastPrompt == "" {
						continue
					}
					ix.Whys[key] = model.Why{Prompt: lastPrompt, SessionID: ln.SessionID, Cwd: ln.Cwd, At: ln.Timestamp}
				}
			}
		case "assistant":
			for _, b := range blocks {
				if b.Type == "tool_use" && isPRCreate(b.Input) {
					creates[b.ID] = true
				}
			}
		}
	}
	mark := fileMark{Offset: offset, LastPrompt: lastPrompt}
	for id := range creates {
		mark.Creates = append(mark.Creates, id)
	}
	return mark, nil
}

// promptText returns a real human prompt, skipping tool results and
// harness-injected messages (which start with "<").
func promptText(text string, blocks []block) string {
	if text != "" {
		return clean(text)
	}
	var parts []string
	for _, b := range blocks {
		if b.Type == "tool_result" {
			return ""
		}
		if b.Type == "text" {
			parts = append(parts, b.Text)
		}
	}
	return clean(strings.Join(parts, "\n"))
}

func clean(s string) string {
	s = strings.TrimSpace(s)
	if s == "" || strings.HasPrefix(s, "<") || strings.HasPrefix(s, "Caveat:") {
		return ""
	}
	return s
}

func isPRCreate(input json.RawMessage) bool {
	var in struct {
		Command string `json:"command"`
	}
	if json.Unmarshal(input, &in) != nil {
		return false
	}
	c := strings.Join(strings.Fields(in.Command), " ")
	return strings.Contains(c, "pr create")
}

func resultText(raw json.RawMessage) string {
	var s string
	if json.Unmarshal(raw, &s) == nil {
		return s
	}
	var blocks []block
	if json.Unmarshal(raw, &blocks) == nil {
		var parts []string
		for _, b := range blocks {
			parts = append(parts, b.Text)
		}
		return strings.Join(parts, "\n")
	}
	return string(raw)
}
