package main

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"sync"
	"syscall"
	"time"
)

type Session struct {
	ID      string    `json:"id"`
	Cwd     string    `json:"cwd"`
	Title   string    `json:"title"`
	Prompt  string    `json:"prompt"`
	Updated time.Time `json:"updated"`
	Live    bool      `json:"live"`
	Status  string    `json:"status"`
	PID     int       `json:"pid"`
	Agents  []Agent   `json:"-"`
	// Scripted marks a headless run (claude -p, the SDK) rather than one a
	// person typed into.
	Scripted bool `json:"scripted"`
}

// Agent is a subagent a session spawned, from <session>/subagents/.
type Agent struct {
	ID          string
	Name        string
	Description string
	Type        string
	ParentID    string // the agent that spawned it; empty when the session did
	Workflow    string
	Depth       int // 0 for an agent the session spawned
	Updated     time.Time
	Path        string
}

// IsActive reports whether the agent wrote to its transcript in the last
// minute and a half: as close to "running" as the files say.
func (a Agent) IsActive(now time.Time) bool {
	return now.Sub(a.Updated) < 90*time.Second
}

type Store struct {
	ClaudeDir string
	CacheFile string
}

func DefaultStore() Store {
	claude := os.Getenv("CLAUDE_CONFIG_DIR")
	if claude == "" {
		claude = filepath.Join(home(), ".claude")
	}
	cache := os.Getenv("XDG_CACHE_HOME")
	if cache == "" {
		cache = filepath.Join(home(), ".cache")
	}
	return Store{ClaudeDir: claude, CacheFile: filepath.Join(cache, "cctree", "index-v2.json")}
}

// Load returns every session with a transcript, plus live ones that have
// not written one yet, newest first.
func (s Store) Load() ([]Session, error) {
	live := s.Live()
	cache := s.readCache()
	files, err := filepath.Glob(filepath.Join(s.ClaudeDir, "projects", "*", "*.jsonl"))
	if err != nil {
		return nil, err
	}

	type result struct {
		path   string
		entry  cacheEntry
		agents []Agent
	}
	jobs := make(chan string)
	results := make(chan result)
	var wg sync.WaitGroup
	for range runtime.NumCPU() {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for path := range jobs {
				st, err := os.Stat(path)
				if err != nil {
					continue
				}
				stamp := [2]int64{st.Size(), st.ModTime().UnixMilli()}
				agents := loadAgents(strings.TrimSuffix(path, ".jsonl") + "/subagents")
				if hit, ok := cache[path]; ok && hit.Stamp == stamp {
					results <- result{path, hit, agents}
					continue
				}
				info, err := readTranscript(path, st.Size())
				if err != nil {
					continue
				}
				results <- result{path, cacheEntry{Stamp: stamp, Info: info}, agents}
			}
		}()
	}
	go func() {
		for _, f := range files {
			jobs <- f
		}
		close(jobs)
		wg.Wait()
		close(results)
	}()

	fresh := map[string]cacheEntry{}
	folders := newFolderResolver()
	var sessions []Session
	for r := range results {
		fresh[r.path] = r.entry
		id := strings.TrimSuffix(filepath.Base(r.path), ".jsonl")
		info := r.entry.Info
		on, isLive := live[id]
		delete(live, id)
		if info.Sidechain || (info.Prompt == "" && !isLive) {
			continue
		}
		session := Session{
			ID:       id,
			Cwd:      folders.resolve(firstOf(info.Cwd, on.Cwd), filepath.Base(filepath.Dir(r.path))),
			Title:    firstOf(info.Custom, info.Title, info.Prompt, "(untitled)"),
			Prompt:   info.Prompt,
			Updated:  time.UnixMilli(r.entry.Stamp[1]),
			Agents:   r.agents,
			Scripted: info.Entrypoint == "sdk-cli" || strings.HasPrefix(info.Entrypoint, "sdk-"),
		}
		if isLive {
			session.Live, session.Status, session.PID = true, on.Status, on.PID
		}
		sessions = append(sessions, session)
	}
	for id, on := range live {
		if on.Kind == "bg" && on.Status == "idle" {
			continue // pre-warmed spare, never used
		}
		sessions = append(sessions, Session{ID: id, Cwd: firstOf(on.Cwd, "?"), Title: "(new session)",
			Updated: time.UnixMilli(on.StartedAt), Live: true, Status: on.Status, PID: on.PID})
	}
	s.writeCache(fresh)
	sort.Slice(sessions, func(i, j int) bool { return sessions[i].Updated.After(sessions[j].Updated) })
	return sessions, nil
}

type LiveSession struct {
	PID       int    `json:"pid"`
	SessionID string `json:"sessionId"`
	Cwd       string `json:"cwd"`
	Status    string `json:"status"`
	Kind      string `json:"kind"`
	StartedAt int64  `json:"startedAt"`
}

// Live reads the registry of running sessions, keyed by session id.
func (s Store) Live() map[string]LiveSession {
	found := map[string]LiveSession{}
	files, _ := filepath.Glob(filepath.Join(s.ClaudeDir, "sessions", "*.json"))
	for _, f := range files {
		data, err := os.ReadFile(f)
		if err != nil {
			continue
		}
		var row LiveSession
		if json.Unmarshal(data, &row) != nil || row.SessionID == "" || !alive(row.PID) {
			continue
		}
		if row.Status == "" {
			row.Status = "running"
		}
		found[row.SessionID] = row
	}
	return found
}

// Private

// loadAgents reads every subagent's meta file under dir, workflow runs
// included, ordered so each agent follows the one that spawned it.
func loadAgents(dir string) []Agent {
	var agents []Agent
	_ = filepath.WalkDir(dir, func(path string, d os.DirEntry, err error) error {
		if err != nil || d.IsDir() || !strings.HasSuffix(path, ".jsonl") || !strings.HasPrefix(d.Name(), "agent-") {
			return nil
		}
		st, err := d.Info()
		if err != nil {
			return nil
		}
		agent := Agent{ID: strings.TrimSuffix(strings.TrimPrefix(d.Name(), "agent-"), ".jsonl"), Updated: st.ModTime(), Path: path}
		var meta struct {
			AgentType     string `json:"agentType"`
			Description   string `json:"description"`
			Name          string `json:"name"`
			ParentAgentID string `json:"parentAgentId"`
		}
		if data, err := os.ReadFile(strings.TrimSuffix(path, ".jsonl") + ".meta.json"); err == nil && json.Unmarshal(data, &meta) == nil {
			agent.Name, agent.Description, agent.Type, agent.ParentID = oneLine(meta.Name), oneLine(meta.Description), meta.AgentType, meta.ParentAgentID
		}
		if rel, err := filepath.Rel(dir, path); err == nil && strings.HasPrefix(rel, "workflows/") {
			agent.Workflow = strings.SplitN(strings.TrimPrefix(rel, "workflows/"), "/", 2)[0]
		}
		agents = append(agents, agent)
		return nil
	})
	return orderAgents(groupWorkflows(agents))
}

// folderResolver finds where a session belongs on this machine. A transcript
// copied from another machine keeps that machine's cwd (/Users/kate/...), so
// when the recorded folder is missing it decodes the project folder the
// transcript is filed under (-home-k-rock-playa) back to a real path.
type folderResolver struct {
	exists  map[string]bool
	decoded map[string]string
	entries map[string][]string
}

func newFolderResolver() *folderResolver {
	return &folderResolver{exists: map[string]bool{}, decoded: map[string]string{}, entries: map[string][]string{}}
}

func (r *folderResolver) resolve(cwd, projectKey string) string {
	if cwd != "" && r.isDir(cwd) {
		return cwd
	}
	path, ok := r.decoded[projectKey]
	if !ok {
		path = r.decode(projectKey)
		r.decoded[projectKey] = path
	}
	return firstOf(path, cwd, "?")
}

// decode turns a project key back into an existing directory. The key is the
// path with every non-alphanumeric character replaced by "-", so each "-" is
// a "/", or a "-", ".", "_" or " " inside a name; the file system decides.
func (r *folderResolver) decode(key string) string {
	tokens := strings.Split(strings.TrimPrefix(key, "-"), "-")
	var found string
	var walk func(i int, dir, name string)
	walk = func(i int, dir, name string) {
		if found != "" {
			return
		}
		if i == len(tokens) {
			if p := dir + "/" + name; r.isDir(p) {
				found = p
			}
			return
		}
		tok := tokens[i]
		if p := dir + "/" + name; name != "" && r.isDir(p) {
			walk(i+1, p, tok)
		}
		for _, sep := range []string{"-", ".", "_", " "} {
			if next := name + sep + tok; r.hasPrefix(dir, next) {
				walk(i+1, dir, next)
			}
		}
	}
	if len(tokens) > 0 {
		walk(1, "", tokens[0])
	}
	return found
}

func (r *folderResolver) isDir(path string) bool {
	is, ok := r.exists[path]
	if !ok {
		st, err := os.Stat(path)
		is = err == nil && st.IsDir()
		r.exists[path] = is
	}
	return is
}

func (r *folderResolver) hasPrefix(dir, prefix string) bool {
	names, ok := r.entries[dir]
	if !ok {
		list, _ := os.ReadDir(firstOf(dir, "/"))
		for _, e := range list {
			names = append(names, e.Name())
		}
		r.entries[dir] = names
	}
	for _, n := range names {
		if strings.HasPrefix(n, prefix) {
			return true
		}
	}
	return false
}

// groupWorkflows gives each workflow run a row of its own, parenting the
// agents it ran that no other agent spawned.
func groupWorkflows(agents []Agent) []Agent {
	runs := map[string]*Agent{}
	known := map[string]bool{}
	for _, a := range agents {
		known[a.ID] = true
	}
	for i := range agents {
		a := &agents[i]
		if a.Workflow == "" || known[a.ParentID] {
			continue
		}
		run, ok := runs[a.Workflow]
		if !ok {
			run = &Agent{ID: "workflow:" + a.Workflow, Name: "workflow " + a.Workflow, Type: "workflow", Workflow: a.Workflow, Updated: a.Updated, Path: filepath.Dir(a.Path)}
			runs[a.Workflow] = run
		}
		if a.Updated.After(run.Updated) {
			run.Updated = a.Updated
		}
		a.ParentID = run.ID
	}
	for _, run := range runs {
		count := 0
		for _, a := range agents {
			if a.ParentID == run.ID {
				count++
			}
		}
		run.Description = fmt.Sprintf("%d agents", count)
		agents = append(agents, *run)
	}
	return agents
}

// orderAgents sorts agents oldest first, each followed by its own children.
func orderAgents(agents []Agent) []Agent {
	sort.Slice(agents, func(i, j int) bool { return agents[i].Updated.Before(agents[j].Updated) })
	known := map[string]bool{}
	for _, a := range agents {
		known[a.ID] = true
	}
	children := map[string][]Agent{}
	for _, a := range agents {
		parent := a.ParentID
		if !known[parent] {
			parent = ""
		}
		children[parent] = append(children[parent], a)
	}
	var out []Agent
	var walk func(string, int)
	walk = func(parent string, depth int) {
		for _, a := range children[parent] {
			a.Depth = depth
			out = append(out, a)
			walk(a.ID, depth+1)
		}
	}
	walk("", 0)
	return out
}

type transcriptInfo struct {
	Cwd        string `json:"cwd"`
	Title      string `json:"title"`
	Custom     string `json:"custom"`
	Prompt     string `json:"prompt"`
	Sidechain  bool   `json:"sidechain"`
	Entrypoint string `json:"entrypoint"`
}

type cacheEntry struct {
	Stamp [2]int64       `json:"stamp"`
	Info  transcriptInfo `json:"info"`
}

const chunk = 256 * 1024

type row struct {
	Type        string          `json:"type"`
	Cwd         string          `json:"cwd"`
	AITitle     string          `json:"aiTitle"`
	CustomTitle string          `json:"customTitle"`
	AgentName   string          `json:"agentName"`
	Summary     string          `json:"summary"`
	IsMeta      bool            `json:"isMeta"`
	IsSidechain bool            `json:"isSidechain"`
	Entrypoint  string          `json:"entrypoint"`
	Message     json.RawMessage `json:"message"`
}

// readTranscript reads the head and tail of a transcript: the first prompt
// and cwd sit at the start, titles are re-appended near the end.
func readTranscript(path string, size int64) (transcriptInfo, error) {
	f, err := os.Open(path)
	if err != nil {
		return transcriptInfo{}, err
	}
	defer f.Close()
	head := make([]byte, min(size, chunk))
	if _, err := io.ReadFull(f, head); err != nil {
		return transcriptInfo{}, err
	}
	blob := head
	if size > chunk {
		tail := make([]byte, min(size-chunk, chunk))
		if _, err := f.ReadAt(tail, size-int64(len(tail))); err != nil && !errors.Is(err, io.EOF) {
			return transcriptInfo{}, err
		}
		if i := bytes.IndexByte(tail, '\n'); i >= 0 {
			tail = tail[i+1:]
		}
		blob = append(append(head, '\n'), tail...)
	}
	return parseTranscript(blob), nil
}

func parseTranscript(blob []byte) transcriptInfo {
	var info transcriptInfo
	scanner := bufio.NewScanner(bytes.NewReader(blob))
	scanner.Buffer(make([]byte, 0, 64*1024), len(blob)+1)
	sawPrompt := false
	for scanner.Scan() {
		line := scanner.Bytes()
		if len(line) == 0 || line[0] != '{' {
			continue
		}
		var r row
		if json.Unmarshal(line, &r) != nil {
			continue
		}
		if info.Cwd == "" && r.Cwd != "" {
			info.Cwd = r.Cwd
		}
		if info.Entrypoint == "" && r.Entrypoint != "" {
			info.Entrypoint = r.Entrypoint
		}
		switch r.Type {
		case "ai-title":
			if r.AITitle != "" {
				info.Title = r.AITitle
			}
		case "custom-title":
			if r.CustomTitle != "" {
				info.Custom = r.CustomTitle
			}
		case "agent-name":
			if r.AgentName != "" && info.Custom == "" {
				info.Custom = r.AgentName
			}
		case "summary":
			if r.Summary != "" && info.Title == "" {
				info.Title = r.Summary
			}
		case "user":
			if sawPrompt || r.IsMeta {
				continue
			}
			if r.IsSidechain {
				info.Sidechain = true
			}
			if text := messageText(r.Message); !isNoise(text) {
				info.Prompt = truncate(strings.Join(strings.Fields(text), " "), 200)
				sawPrompt = true
			}
		}
	}
	return info
}

func messageText(raw json.RawMessage) string {
	var msg struct {
		Content json.RawMessage `json:"content"`
	}
	if json.Unmarshal(raw, &msg) != nil {
		return ""
	}
	var text string
	if json.Unmarshal(msg.Content, &text) == nil {
		return text
	}
	var blocks []struct {
		Type string `json:"type"`
		Text string `json:"text"`
	}
	if json.Unmarshal(msg.Content, &blocks) == nil {
		for _, b := range blocks {
			if b.Type == "text" {
				return b.Text
			}
		}
	}
	return ""
}

func isNoise(text string) bool {
	t := strings.TrimSpace(text)
	return t == "" || strings.HasPrefix(t, "<") || strings.HasPrefix(t, "[Request interrupted") || strings.HasPrefix(t, "Caveat:")
}

func (s Store) readCache() map[string]cacheEntry {
	cache := map[string]cacheEntry{}
	if data, err := os.ReadFile(s.CacheFile); err == nil {
		_ = json.Unmarshal(data, &cache)
	}
	return cache
}

func (s Store) writeCache(cache map[string]cacheEntry) {
	data, err := json.Marshal(cache)
	if err != nil {
		return
	}
	_ = os.MkdirAll(filepath.Dir(s.CacheFile), 0o755)
	tmp := s.CacheFile + ".tmp"
	if os.WriteFile(tmp, data, 0o644) == nil {
		_ = os.Rename(tmp, s.CacheFile)
	}
}

// Helpers

func alive(pid int) bool {
	if pid <= 0 {
		return false
	}
	err := syscall.Kill(pid, 0)
	return err == nil || errors.Is(err, syscall.EPERM)
}

func oneLine(s string) string {
	return strings.Join(strings.Fields(s), " ")
}

func home() string {
	h, _ := os.UserHomeDir()
	return h
}

func firstOf(values ...string) string {
	for _, v := range values {
		if v != "" {
			return v
		}
	}
	return ""
}

func truncate(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:max(1, n-1)]) + "…"
}
