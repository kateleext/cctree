package main

import (
	"fmt"
	"image/color"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"
)

const runningPath = "\x00running"

type pane int

const (
	treePane pane = iota
	listPane
)

// item is one row of the right pane: a subfolder, a session, or one of a
// session's subagents (session set too, so actions reach its parent).
type item struct {
	folder  *Folder
	session *Session
	agent   *Agent
}

type treeRow struct {
	folder *Folder
	depth  int
}

type Model struct {
	store   Store
	homeDir string
	now     func() time.Time

	sessions    []Session
	root        *Folder
	running     *Folder
	rows        []treeRow
	expanded    map[string]bool
	shownAgents map[string]bool

	focus      pane
	treeCursor int
	listCursor int

	filter      string
	isFiltering bool
	isLiveOnly  bool
	isScripted  bool // showing headless claude -p / SDK runs
	isLoading   bool

	width, height int
	message       string

	// Resume is set when the person picked a past session: main execs it
	// once the TUI has given the terminal back.
	Resume *Session
}

type loadedMsg struct {
	sessions []Session
	err      error
}

type liveMsg map[string]LiveSession

type tickMsg struct{}

func NewModel(store Store) Model {
	return Model{store: store, homeDir: home(), now: time.Now, expanded: map[string]bool{"~": true, runningPath: true}, shownAgents: map[string]bool{}, isLoading: true}
}

func (m Model) Init() tea.Cmd {
	return tea.Batch(m.load(), tick())
}

func (m Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width, m.height = msg.Width, msg.Height
	case loadedMsg:
		m.isLoading = false
		if msg.err != nil {
			m.message = "scan failed: " + msg.err.Error()
			return m, nil
		}
		m.sessions = msg.sessions
		m.rebuild()
	case liveMsg:
		m.applyLive(msg)
	case tickMsg:
		store := m.store
		return m, tea.Batch(tick(), func() tea.Msg { return liveMsg(store.Live()) })
	case tea.KeyPressMsg:
		return m.key(msg)
	case tea.MouseWheelMsg:
		if msg.Button == tea.MouseWheelUp {
			m.move(-3)
		} else if msg.Button == tea.MouseWheelDown {
			m.move(3)
		}
	}
	return m, nil
}

func (m Model) View() tea.View {
	v := tea.NewView(m.render())
	v.AltScreen = true
	v.MouseMode = tea.MouseModeCellMotion
	v.WindowTitle = "cctree"
	return v
}

// Private

func (m Model) key(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	k := msg.String()
	if m.isFiltering {
		switch k {
		case "esc":
			m.isFiltering, m.filter = false, ""
			m.rebuild()
		case "enter", "down", "tab":
			m.isFiltering = false
		case "backspace":
			if r := []rune(m.filter); len(r) > 0 {
				m.filter = string(r[:len(r)-1])
				m.rebuild()
			}
		case "ctrl+c":
			return m, tea.Quit
		default:
			if msg.Text != "" {
				m.filter += msg.Text
				m.rebuild()
			}
		}
		return m, nil
	}

	switch k {
	case "q", "ctrl+c":
		return m, tea.Quit
	case "/":
		m.isFiltering = true
	case "a":
		m.isLiveOnly = !m.isLiveOnly
		m.rebuild()
	case "s":
		m.isScripted = !m.isScripted
		m.rebuild()
	case "r":
		m.isLoading, m.message = true, ""
		return m, m.load()
	case "tab":
		m.focus = 1 - m.focus
	case "up", "k":
		m.move(-1)
	case "down", "j":
		m.move(1)
	case "pgup":
		m.move(-m.bodyHeight())
	case "pgdown":
		m.move(m.bodyHeight())
	case "home", "g":
		m.move(-1 << 20)
	case "end", "G":
		m.move(1 << 20)
	case "left", "h":
		if m.focus == treePane {
			m.collapse()
		} else if it, ok := m.current(); ok && it.agent != nil {
			delete(m.shownAgents, it.session.ID)
			m.selectSession(it.session.ID)
		} else if ok && it.session != nil && m.shownAgents[it.session.ID] {
			delete(m.shownAgents, it.session.ID)
		} else {
			m.focus = treePane
		}
	case "esc":
		if m.focus == listPane {
			m.focus = treePane
		} else if m.filter != "" {
			m.filter = ""
			m.rebuild()
		}
	case "right", "l", "space":
		if m.focus == treePane {
			m.expand()
		} else if it, ok := m.current(); ok && it.folder != nil {
			m.enterFolder(it.folder)
		} else if ok && it.agent == nil && it.session != nil && len(it.session.Agents) > 0 {
			m.shownAgents[it.session.ID] = !m.shownAgents[it.session.ID]
		}
	case "enter":
		if m.focus == treePane {
			if f := m.folder(); f != nil && len(f.Folders) > 0 && !m.expanded[f.Path] {
				m.expanded[f.Path] = true
				m.flatten()
			} else {
				m.focus, m.listCursor = listPane, 0
			}
			return m, nil
		}
		if it, ok := m.current(); ok && it.folder != nil {
			m.enterFolder(it.folder)
			return m, nil
		}
		return m.open(false)
	case "o":
		if m.focus == listPane {
			return m.open(true)
		}
	}
	return m, nil
}

func (m Model) open(isNewWindow bool) (tea.Model, tea.Cmd) {
	s, ok := m.session()
	if !ok {
		return m, nil
	}
	if s.Live {
		m.message = FocusLive(s)
		return m, nil
	}
	if isNewWindow {
		if err := OpenTerminal(s.Cwd, "claude", "--resume", s.ID); err != nil {
			m.message = err.Error()
		} else {
			m.message = "opened " + truncate(s.Title, 40) + " in a new window"
		}
		return m, nil
	}
	m.Resume = &s
	return m, tea.Quit
}

func (m *Model) move(delta int) {
	if m.focus == treePane {
		m.treeCursor = clamp(m.treeCursor+delta, 0, len(m.rows)-1)
		m.listCursor = 0
	} else {
		m.listCursor = clamp(m.listCursor+delta, 0, len(m.list())-1)
	}
}

// enterFolder selects a subfolder of the current folder in the tree.
func (m *Model) enterFolder(f *Folder) {
	if cur := m.folder(); cur != nil {
		m.expanded[cur.Path] = true
	}
	m.flatten()
	for i, r := range m.rows {
		if r.folder.Path == f.Path {
			m.treeCursor, m.listCursor = i, 0
			return
		}
	}
}

func (m *Model) selectSession(id string) {
	for i, it := range m.list() {
		if it.agent == nil && it.session != nil && it.session.ID == id {
			m.listCursor = i
			return
		}
	}
}

func (m *Model) expand() {
	f := m.folder()
	if f == nil {
		return
	}
	if len(f.Folders) == 0 || m.expanded[f.Path] {
		m.focus, m.listCursor = listPane, 0
		return
	}
	m.expanded[f.Path] = true
	m.flatten()
}

func (m *Model) collapse() {
	if m.treeCursor >= len(m.rows) {
		return
	}
	row := m.rows[m.treeCursor]
	if m.expanded[row.folder.Path] && len(row.folder.Folders) > 0 {
		delete(m.expanded, row.folder.Path)
		m.flatten()
		return
	}
	for i := m.treeCursor - 1; i >= 0; i-- {
		if m.rows[i].depth < row.depth {
			m.treeCursor = i
			return
		}
	}
}

func (m *Model) rebuild() {
	var path string
	if f := m.folder(); f != nil {
		path = f.Path
	}
	var shown, live []Session
	for _, s := range m.sessions {
		if m.isLiveOnly && !s.Live {
			continue
		}
		if s.Scripted && !s.Live && !m.isScripted {
			continue
		}
		if m.filter != "" && !matches(s, m.filter, m.homeDir) {
			continue
		}
		shown = append(shown, s)
		if s.Live {
			live = append(live, s)
		}
	}
	m.root = BuildTree(shown, m.homeDir)
	m.running = BuildTree(live, m.homeDir)
	m.running.Path, m.running.Name = runningPath, "● running"
	prefixPaths(m.running.Folders, runningPath+"/")
	m.flatten()
	m.treeCursor = 0
	for i, r := range m.rows {
		if r.folder.Path == path {
			m.treeCursor = i
		}
	}
	m.listCursor = clamp(m.listCursor, 0, len(m.list())-1)
}

func (m *Model) flatten() {
	m.rows = m.rows[:0]
	if m.root == nil {
		return
	}
	isAllOpen := m.filter != "" || m.isLiveOnly
	var walk func(f *Folder, depth int, isOpen bool)
	walk = func(f *Folder, depth int, isOpen bool) {
		for _, c := range f.Folders {
			m.rows = append(m.rows, treeRow{folder: c, depth: depth})
			if isOpen || m.expanded[c.Path] {
				walk(c, depth+1, isOpen)
			}
		}
	}
	// The running section is its own tree of just the folders with live
	// sessions, always open beneath its header.
	if m.running != nil && m.running.Total > 0 {
		m.rows = append(m.rows, treeRow{folder: m.running})
		if m.expanded[runningPath] {
			walk(m.running, 1, true)
		}
	}
	walk(m.root, 0, isAllOpen)
	m.treeCursor = clamp(m.treeCursor, 0, len(m.rows)-1)
}

func (m *Model) applyLive(live map[string]LiveSession) {
	changed := false
	for i := range m.sessions {
		s := &m.sessions[i]
		on, isLive := live[s.ID]
		if s.Live != isLive || (isLive && s.Status != on.Status) {
			s.Live, s.Status, s.PID = isLive, on.Status, on.PID
			changed = true
		}
	}
	if changed {
		m.rebuild()
	}
}

func (m Model) folder() *Folder {
	if m.treeCursor < 0 || m.treeCursor >= len(m.rows) {
		return nil
	}
	return m.rows[m.treeCursor].folder
}

// list is the selected folder's own level: its subfolders, then the
// sessions started in it, each followed by its subagents when shown.
func (m Model) list() []item {
	f := m.folder()
	if f == nil {
		return nil
	}
	var out []item
	for _, c := range f.Folders {
		out = append(out, item{folder: c})
	}
	for i := range f.Sessions {
		s := &f.Sessions[i]
		out = append(out, item{session: s})
		if m.shownAgents[s.ID] {
			for j := range s.Agents {
				out = append(out, item{session: s, agent: &s.Agents[j]})
			}
		}
	}
	return out
}

func (m Model) current() (item, bool) {
	list := m.list()
	if m.listCursor < 0 || m.listCursor >= len(list) {
		return item{}, false
	}
	return list[m.listCursor], true
}

func (m Model) session() (Session, bool) {
	it, ok := m.current()
	if !ok || it.session == nil {
		return Session{}, false
	}
	return *it.session, true
}

func (m Model) load() tea.Cmd {
	store := m.store
	return func() tea.Msg {
		sessions, err := store.Load()
		return loadedMsg{sessions, err}
	}
}

// Drawing

var (
	accent   = lipgloss.Color("4")
	liveCol  = lipgloss.Color("2")
	busyCol  = lipgloss.Color("3")
	muted    = lipgloss.NewStyle().Foreground(lipgloss.Color("8"))
	bold     = lipgloss.NewStyle().Bold(true)
	selected = lipgloss.NewStyle().Reverse(true)
	dimSel   = lipgloss.NewStyle().Bold(true).Foreground(accent)
)

func (m Model) bodyHeight() int {
	return max(3, m.height-4-detailHeight)
}

const detailHeight = 6

func (m Model) render() string {
	if m.width < 50 || m.height < 14 {
		return "cctree needs at least 50×14"
	}
	header := m.header()
	leftW := clamp(m.width/3, 28, 48)
	rightW := m.width - leftW - 3
	h := m.bodyHeight()

	left := m.renderTree(leftW, h)
	right := m.renderList(rightW, h)
	sep := muted.Render(strings.TrimSuffix(strings.Repeat(" │ \n", h), "\n"))
	body := lipgloss.JoinHorizontal(lipgloss.Top, left, sep, right)

	return strings.Join([]string{header, "", body, m.renderDetail(m.width), m.footer()}, "\n")
}

func (m Model) header() string {
	live, scripted := 0, 0
	for _, s := range m.sessions {
		if s.Live {
			live++
		} else if s.Scripted {
			scripted++
		}
	}
	shown := len(m.sessions)
	if !m.isScripted {
		shown -= scripted
	}
	title := bold.Render("Claude sessions")
	stats := muted.Render(fmt.Sprintf("  %d sessions · ", shown)) + lipgloss.NewStyle().Foreground(liveCol).Render(fmt.Sprintf("%d running", live))
	if scripted > 0 {
		verb := map[bool]string{true: "shown", false: "hidden"}[m.isScripted]
		stats += muted.Render(fmt.Sprintf(" · %d scripted %s", scripted, verb))
	}
	if m.isLoading {
		stats += muted.Render("  · scanning…")
	}
	var right string
	switch {
	case m.isFiltering:
		right = "/" + m.filter + "█"
	case m.filter != "":
		right = muted.Render("filter: ") + m.filter
	}
	if m.isLiveOnly {
		right += lipgloss.NewStyle().Foreground(liveCol).Render("  [running only]")
	}
	line := title + stats
	gap := m.width - lipgloss.Width(line) - lipgloss.Width(right)
	return line + strings.Repeat(" ", max(1, gap)) + right
}

func (m Model) renderTree(w, h int) string {
	top := scrollTop(m.treeCursor, h, len(m.rows))
	var lines []string
	for i := top; i < min(len(m.rows), top+h); i++ {
		row := m.rows[i]
		f := row.folder
		isRunning := isRunningPath(f.Path)
		marker := "  "
		if len(f.Folders) > 0 {
			marker = "▸ "
			if m.expanded[f.Path] || m.filter != "" || m.isLiveOnly || (isRunning && f.Path != runningPath) {
				marker = "▾ "
			}
		}
		name := strings.Repeat("  ", row.depth) + marker + f.Name
		count := fmt.Sprint(f.Total)
		liveTxt := ""
		if isRunning {
			count = ""
			if f.Path != runningPath {
				liveTxt = fmt.Sprintf("●%d", f.Live)
			} else {
				count = fmt.Sprint(f.Live)
			}
		} else if f.Live > 0 {
			liveTxt = fmt.Sprintf("●%d ", f.Live)
		}
		room := w - len([]rune(count)) - len([]rune(liveTxt)) - 1
		name = ansi.Truncate(name, room, "…")
		pad := strings.Repeat(" ", max(1, w-lipgloss.Width(name)-len([]rune(liveTxt))-len(count)))
		plain := name + pad + liveTxt + count
		switch {
		case i == m.treeCursor && m.focus == treePane:
			lines = append(lines, selected.Render(plain))
		case i == m.treeCursor:
			lines = append(lines, dimSel.Render(plain))
		case f.Path == runningPath:
			lines = append(lines, lipgloss.NewStyle().Foreground(liveCol).Bold(true).Render(plain))
		default:
			lines = append(lines, name+pad+lipgloss.NewStyle().Foreground(liveCol).Render(liveTxt)+muted.Render(count))
		}
	}
	if len(m.rows) == 0 {
		lines = append(lines, muted.Render(map[bool]string{true: "scanning…", false: "no sessions"}[m.isLoading]))
	}
	return fill(lines, w, h)
}

func (m Model) renderList(w, h int) string {
	f := m.folder()
	list := m.list()
	var lines []string
	if f != nil {
		label := strings.TrimPrefix(f.Path, runningPath+"/")
		if f.Path == runningPath {
			label = "running now"
		}
		here := fmt.Sprintf("  %d here", len(f.Sessions))
		if len(f.Folders) > 0 {
			here += fmt.Sprintf(" · %d below", f.Total-len(f.Sessions))
		}
		lines = append(lines, bold.Render(ansi.Truncate(label, w-24, "…"))+muted.Render(here))
	}
	h--
	top := scrollTop(m.listCursor, h, len(list))
	for i := top; i < min(len(list), top+h); i++ {
		left, right, markCol, isDim := m.itemText(f, list[i])
		room := w - 1 - len([]rune(right))
		left = ansi.Truncate(left, room, "…")
		pad := strings.Repeat(" ", max(1, w-lipgloss.Width(left)-len([]rune(right))))
		switch {
		case i == m.listCursor && m.focus == listPane:
			lines = append(lines, selected.Render(left+pad+right))
		case i == m.listCursor:
			lines = append(lines, dimSel.Render(left+pad+right))
		default:
			mark, rest, _ := strings.Cut(left, " ")
			if strings.HasPrefix(left, " ") {
				mark, rest = "", left
			}
			style := lipgloss.NewStyle()
			if isDim {
				style = style.Foreground(lipgloss.Color("7"))
			}
			lines = append(lines, lipgloss.NewStyle().Foreground(markCol).Render(mark)+map[bool]string{true: " ", false: ""}[mark != ""]+style.Render(rest)+pad+muted.Render(right))
		}
	}
	if len(list) == 0 && f != nil {
		lines = append(lines, muted.Render("no sessions at this level"))
	}
	return fill(lines, w, h+1)
}

// itemText lays out one right-pane row: the text, the right-hand column,
// the color of its leading mark and whether it is drawn dim.
func (m Model) itemText(f *Folder, it item) (left, right string, markCol color.Color, isDim bool) {
	now := m.now()
	switch {
	case it.folder != nil:
		right = fmt.Sprint(it.folder.Total)
		if it.folder.Live > 0 {
			right = fmt.Sprintf("●%d %s", it.folder.Live, right)
		}
		return "▸ " + it.folder.Name + "/", right, accent, false
	case it.agent != nil:
		a := it.agent
		label := firstOf(a.Name, a.Type, a.ID)
		if a.Description != "" && a.Description != label {
			label += " — " + a.Description
		}
		mark, col := "↳", lipgloss.Color("8")
		right = ago(a.Updated, now)
		if it.session.Live && a.IsActive(now) {
			mark, col, right = "◉", busyCol, "working"
		}
		return strings.Repeat("  ", a.Depth+1) + mark + " " + label, right, col, true
	default:
		s := it.session
		mark, col, when := "○", lipgloss.Color("8"), ago(s.Updated, now)
		if s.Live {
			mark, col, when = "●", liveCol, s.Status
			if s.Status == "busy" {
				col = busyCol
			}
		}
		text := s.Title
		if n := agentCount(s.Agents); n > 0 {
			fold := "▸"
			if m.shownAgents[s.ID] {
				fold = "▾"
			}
			when = fmt.Sprintf("%s %d agent%s  %s", fold, n, map[bool]string{true: "", false: "s"}[n == 1], when)
		}
		return mark + " " + text, when, col, !s.Live
	}
}

func (m Model) renderDetail(w int) string {
	rule := muted.Render(strings.Repeat("─", w))
	if it, ok := m.current(); ok && it.agent != nil {
		a := it.agent
		kind := "subagent · " + firstOf(a.Type, "agent")
		if a.Workflow != "" {
			kind += " · workflow " + a.Workflow
		}
		lines := []string{
			rule,
			bold.Render(ansi.Truncate(firstOf(a.Name, a.Description, a.ID), w, "…")) + "  " + muted.Render(kind+" · last write "+ago(a.Updated, m.now())+" ago"),
			ansi.Truncate(a.Description, w, "…"),
			muted.Render(ansi.Truncate("in "+it.session.Title+"  ·  "+DisplayPath(a.Path, m.homeDir), w, "…")),
		}
		return fill(lines, w, detailHeight)
	}
	if it, ok := m.current(); ok && it.folder != nil {
		f := it.folder
		lines := []string{rule, bold.Render(f.Path) + "  " + muted.Render(fmt.Sprintf("%d sessions, %d running, last active %s ago", f.Total, f.Live, ago(f.Latest, m.now())))}
		return fill(lines, w, detailHeight)
	}
	s, ok := m.session()
	if !ok {
		if m.message != "" {
			return fill([]string{rule, m.message}, w, detailHeight)
		}
		return fill([]string{rule}, w, detailHeight)
	}
	state := muted.Render("last active " + s.Updated.Format("Mon Jan 2 15:04") + " (" + ago(s.Updated, m.now()) + " ago)")
	if s.Live {
		state = lipgloss.NewStyle().Foreground(liveCol).Render(fmt.Sprintf("● %s · pid %d", s.Status, s.PID))
	}
	prompt := s.Prompt
	if prompt == "" {
		prompt = "(no prompt yet)"
	}
	lines := []string{
		rule,
		bold.Render(ansi.Truncate(s.Title, w, "…")) + "  " + state,
		muted.Render(ansi.Truncate(DisplayPath(s.Cwd, m.homeDir)+"  ·  "+s.ID, w, "…")),
		ansi.Truncate("› "+prompt, w, "…"),
	}
	if m.message != "" {
		lines = append(lines, lipgloss.NewStyle().Foreground(accent).Render(ansi.Truncate(m.message, w, "…")))
	}
	return fill(lines, w, detailHeight)
}

func (m Model) footer() string {
	keys := "↑↓ move  ←→ fold  tab switch  enter "
	if it, ok := m.current(); m.focus == listPane && ok && it.folder != nil {
		keys += "open folder"
	} else if m.focus == listPane {
		if s, ok := m.session(); ok && s.Live {
			keys += "focus window"
		} else {
			keys += "resume here  o new window"
		}
	} else {
		keys += "open"
	}
	keys += "  / filter  a running only  s scripted  r rescan  q quit"
	return muted.Render(ansi.Truncate(keys, m.width, "…"))
}

// Helpers

func isRunningPath(path string) bool {
	return path == runningPath || strings.HasPrefix(path, runningPath+"/")
}

// prefixPaths moves a tree's folder paths under prefix, so the running
// section's folders never share a key with the full tree's.
func prefixPaths(folders []*Folder, prefix string) {
	for _, f := range folders {
		f.Path = prefix + f.Path
		prefixPaths(f.Folders, prefix)
	}
}

func agentCount(agents []Agent) int {
	n := 0
	for _, a := range agents {
		if a.Type != "workflow" {
			n++
		}
	}
	return n
}

func tick() tea.Cmd {
	return tea.Tick(3*time.Second, func(time.Time) tea.Msg { return tickMsg{} })
}

func matches(s Session, query, homeDir string) bool {
	hay := strings.ToLower(s.Title + " " + s.Prompt + " " + DisplayPath(s.Cwd, homeDir) + " " + s.ID)
	for _, word := range strings.Fields(strings.ToLower(query)) {
		if !strings.Contains(hay, word) {
			return false
		}
	}
	return true
}

func ago(t time.Time, now time.Time) string {
	d := now.Sub(t)
	switch {
	case d < time.Minute:
		return "now"
	case d < time.Hour:
		return fmt.Sprintf("%dm", int(d.Minutes()))
	case d < 24*time.Hour:
		return fmt.Sprintf("%dh", int(d.Hours()))
	case d < 7*24*time.Hour:
		return fmt.Sprintf("%dd", int(d.Hours()/24))
	case t.Year() == now.Year():
		return t.Format("Jan 2")
	default:
		return t.Format("Jan 2006")
	}
}

// scrollTop keeps the cursor in the middle of a list taller than its box.
func scrollTop(cursor, height, total int) int {
	if total <= height {
		return 0
	}
	return clamp(cursor-height/2, 0, total-height)
}

func fill(lines []string, w, h int) string {
	for len(lines) < h {
		lines = append(lines, "")
	}
	lines = lines[:h]
	for i, l := range lines {
		if gap := w - lipgloss.Width(l); gap > 0 {
			lines[i] = l + strings.Repeat(" ", gap)
		}
	}
	return strings.Join(lines, "\n")
}

func clamp(v, lo, hi int) int {
	if hi < lo {
		return lo
	}
	return max(lo, min(v, hi))
}
