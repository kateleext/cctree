package main

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"runtime"
	"strconv"
	"strings"
)

// FocusLive brings a running session's terminal to the front: the Hyprland
// window that owns the process, or, inside tmux, the window of a client
// attached to its tmux session (selecting the pane first).
func FocusLive(s Session) string {
	if runtime.GOOS == "darwin" {
		return focusMac(s)
	}
	windows := hyprWindows()
	if windows == nil {
		return fmt.Sprintf("running as pid %d in %s (no Hyprland to focus it)", s.PID, s.Cwd)
	}
	chain := ancestors(s.PID)
	for _, pid := range chain {
		if addr, ok := windows[pid]; ok {
			return focusWindow(addr, s)
		}
	}
	pane, session := tmuxPane(chain)
	if pane == "" {
		return fmt.Sprintf("running as pid %d, but no window found for it", s.PID)
	}
	_ = exec.Command("tmux", "select-window", "-t", pane).Run()
	_ = exec.Command("tmux", "select-pane", "-t", pane).Run()
	for _, client := range tmuxClients(session) {
		for _, pid := range ancestors(client) {
			if addr, ok := windows[pid]; ok {
				return focusWindow(addr, s)
			}
		}
	}
	if err := OpenTerminal(s.Cwd, "tmux", "attach", "-t", session); err != nil {
		return "tmux session " + session + " has no client: " + err.Error()
	}
	return "attached to tmux session " + session + " in a new window"
}

// OpenTerminal runs argv in a new terminal window, detached from this one.
func OpenTerminal(dir string, argv ...string) error {
	if _, err := os.Stat(dir); err != nil {
		dir = home()
	}
	if runtime.GOOS == "darwin" {
		script := fmt.Sprintf(`tell application "Terminal" to do script %q`, "cd "+shellQuote(dir)+" && "+shellJoin(argv))
		return exec.Command("osascript", "-e", script, "-e", `tell application "Terminal" to activate`).Start()
	}
	term, err := exec.LookPath("xdg-terminal-exec")
	if err != nil {
		return fmt.Errorf("no xdg-terminal-exec to open a window")
	}
	args := append([]string{term, "--dir=" + dir}, argv...)
	if uwsm, err := exec.LookPath("uwsm-app"); err == nil {
		args = append([]string{uwsm, "--"}, args...)
	}
	cmd := exec.Command("setsid", args...)
	cmd.Dir = dir
	return cmd.Start()
}

// Private

func focusWindow(addr string, s Session) string {
	if err := exec.Command("hyprctl", "dispatch", "focuswindow", "address:"+addr).Run(); err != nil {
		return "could not focus window: " + err.Error()
	}
	return "focused " + truncate(s.Title, 40)
}

func hyprWindows() map[int]string {
	out, err := exec.Command("hyprctl", "clients", "-j").Output()
	if err != nil {
		return nil
	}
	var clients []struct {
		Address string `json:"address"`
		PID     int    `json:"pid"`
	}
	if json.Unmarshal(out, &clients) != nil {
		return nil
	}
	windows := map[int]string{}
	for _, c := range clients {
		windows[c.PID] = c.Address
	}
	return windows
}

// focusMac selects the session's tmux pane when it has one; macOS gives
// no portable way to raise another app's terminal tab.
func focusMac(s Session) string {
	if pane, session := tmuxPane(ancestors(s.PID)); pane != "" {
		_ = exec.Command("tmux", "select-window", "-t", pane).Run()
		_ = exec.Command("tmux", "select-pane", "-t", pane).Run()
		return "selected its pane in tmux session " + session
	}
	return fmt.Sprintf("running as pid %d in %s; switch to its terminal", s.PID, s.Cwd)
}

func ancestors(pid int) []int {
	var chain []int
	for pid > 1 && len(chain) < 64 {
		chain = append(chain, pid)
		pid = parentOf(pid)
	}
	return chain
}

func parentOf(pid int) int {
	if data, err := os.ReadFile(fmt.Sprintf("/proc/%d/stat", pid)); err == nil {
		// Fields after the parenthesised command name: state, ppid, ...
		fields := strings.Fields(string(data[strings.LastIndexByte(string(data), ')')+1:]))
		if len(fields) >= 2 {
			ppid, _ := strconv.Atoi(fields[1])
			return ppid
		}
		return 0
	}
	out, err := exec.Command("ps", "-o", "ppid=", "-p", strconv.Itoa(pid)).Output()
	if err != nil {
		return 0
	}
	ppid, _ := strconv.Atoi(strings.TrimSpace(string(out)))
	return ppid
}

func shellQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}

func shellJoin(argv []string) string {
	quoted := make([]string, len(argv))
	for i, a := range argv {
		quoted[i] = shellQuote(a)
	}
	return strings.Join(quoted, " ")
}

func tmuxPane(chain []int) (pane, session string) {
	out, err := exec.Command("tmux", "list-panes", "-a", "-F", "#{pane_pid} #{session_name}:#{window_index}.#{pane_index} #{session_name}").Output()
	if err != nil {
		return "", ""
	}
	inChain := map[int]bool{}
	for _, pid := range chain {
		inChain[pid] = true
	}
	for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		f := strings.Fields(line)
		if len(f) != 3 {
			continue
		}
		if pid, _ := strconv.Atoi(f[0]); inChain[pid] {
			return f[1], f[2]
		}
	}
	return "", ""
}

func tmuxClients(session string) []int {
	out, err := exec.Command("tmux", "list-clients", "-t", session, "-F", "#{client_pid}").Output()
	if err != nil {
		return nil
	}
	var pids []int
	for _, line := range strings.Fields(string(out)) {
		if pid, err := strconv.Atoi(line); err == nil {
			pids = append(pids, pid)
		}
	}
	return pids
}
