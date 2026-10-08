package main

import (
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
)

func at(id, cwd string, minsAgo int, live bool) Session {
	return Session{ID: id, Cwd: cwd, Title: "title " + id, Prompt: "prompt " + id,
		Updated: time.Now().Add(-time.Duration(minsAgo) * time.Minute), Live: live, Status: map[bool]string{true: "busy"}[live]}
}

func fixture() []Session {
	return []Session{
		at("a", "/home/me/work/apps/api", 1, true),
		at("b", "/home/me/work/apps/api", 30, false),
		at("c", "/home/me/work/apps/web", 5, false),
		at("d", "/home/me", 60, false),
		at("e", "/tmp/x", 90, false),
	}
}

func TestBuildTreeFoldsChainsAndCounts(t *testing.T) {
	root := BuildTree(fixture(), "/home/me")
	if len(root.Folders) != 2 || root.Folders[0].Name != "~" || root.Folders[1].Name != "/tmp/x" {
		t.Fatalf("top folders: %+v", names(root.Folders))
	}
	homeF := root.Folders[0]
	if homeF.Total != 4 || homeF.Live != 1 {
		t.Fatalf("~ total=%d live=%d", homeF.Total, homeF.Live)
	}
	if got := names(homeF.Folders); len(got) != 1 || got[0] != "work/apps" {
		t.Fatalf("folded: %v", got)
	}
	all := homeF.All()
	if all[0].ID != "a" || all[1].ID != "c" {
		t.Fatalf("running first then newest: %v", ids(all))
	}
}

func TestParseTranscriptSkipsNoiseAndPrefersCustomTitle(t *testing.T) {
	blob := []byte(`{"type":"user","cwd":"/w","isMeta":true,"message":{"content":"meta"}}
{"type":"user","cwd":"/w","message":{"content":"<command-name>/mcp</command-name>"}}
{"type":"user","cwd":"/w","message":{"content":[{"type":"text","text":"fix   the bug"}]}}
{"type":"ai-title","aiTitle":"Bug fix"}
{"type":"custom-title","customTitle":"mine"}`)
	info := parseTranscript(blob)
	if info.Cwd != "/w" || info.Prompt != "fix the bug" || info.Title != "Bug fix" || info.Custom != "mine" {
		t.Fatalf("%+v", info)
	}
}

func TestKeysExpandFilterAndResume(t *testing.T) {
	m := NewModel(Store{})
	m.homeDir = "/home/me"
	next, _ := m.Update(tea.WindowSizeMsg{Width: 120, Height: 30})
	next, _ = next.Update(loadedMsg{sessions: fixture()})
	m = next.(Model)
	view := ansi.Strip(m.render())
	if !contains(view, "● running") || !contains(view, "work/apps") {
		t.Fatalf("first view:\n%s", view)
	}

	m = press(m, "/", "w", "e", "b", "enter")
	if got := ids(m.root.All()); len(got) != 1 || got[0] != "c" {
		t.Fatalf("filter well: %v", got)
	}

	m = press(m, "esc", "/", "esc") // clear the filter
	m = press(m, "down", "tab")     // ~ : its subfolder, then session d
	if it, _ := m.current(); it.folder == nil || it.folder.Name != "work/apps" {
		t.Fatalf("first item under ~ should be its subfolder, got %+v", it)
	}
	m = press(m, "down", "enter")
	if m.Resume == nil || m.Resume.ID != "d" {
		t.Fatalf("expected to resume d, got %+v", m.Resume)
	}
}

func TestSubfoldersAndAgentsNest(t *testing.T) {
	sessions := fixture()
	sessions[1].Agents = orderAgents([]Agent{
		{ID: "x", Name: "explore", Updated: time.Now().Add(-3 * time.Minute)},
		{ID: "y", Name: "fork", ParentID: "x", Updated: time.Now().Add(-2 * time.Minute)},
	})
	m := NewModel(Store{})
	m.homeDir = "/home/me"
	next, _ := m.Update(tea.WindowSizeMsg{Width: 120, Height: 30})
	next, _ = next.Update(loadedMsg{sessions: sessions})
	m = next.(Model)
	m = press(m, "down", "tab", "enter") // ~ → work/apps
	if f := m.folder(); f == nil || f.Name != "work/apps" {
		t.Fatalf("entered %+v", m.folder())
	}
	m = press(m, "enter") // → api
	if got := len(m.list()); got != 2 {
		t.Fatalf("api lists its 2 sessions only, got %d", got)
	}
	m = press(m, "down", "right")
	list := m.list()
	if len(list) != 4 || list[3].agent.ID != "y" || list[3].agent.Depth != 1 {
		t.Fatalf("agents nested under b: %+v", list)
	}
	if !strings.Contains(ansi.Strip(m.render()), "2 agents") {
		t.Fatal("session row should count its agents")
	}
}

func press(m Model, keys ...string) Model {
	for _, k := range keys {
		var msg tea.KeyPressMsg
		switch k {
		case "enter":
			msg = tea.KeyPressMsg{Code: tea.KeyEnter}
		case "esc":
			msg = tea.KeyPressMsg{Code: tea.KeyEscape}
		case "tab":
			msg = tea.KeyPressMsg{Code: tea.KeyTab}
		case "down":
			msg = tea.KeyPressMsg{Code: tea.KeyDown}
		default:
			msg = tea.KeyPressMsg{Code: []rune(k)[0], Text: k}
		}
		next, _ := m.Update(msg)
		m = next.(Model)
	}
	return m
}

func names(fs []*Folder) []string {
	var out []string
	for _, f := range fs {
		out = append(out, f.Name)
	}
	return out
}

func ids(ss []Session) []string {
	var out []string
	for _, s := range ss {
		out = append(out, s.ID)
	}
	return out
}

func contains(s, sub string) bool { return strings.Contains(s, sub) }
