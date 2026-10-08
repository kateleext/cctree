package main

import (
	"fmt"
	"os"
	"os/exec"
	"syscall"

	tea "charm.land/bubbletea/v2"
)

var version = "dev"

func main() {
	if len(os.Args) > 1 {
		switch os.Args[1] {
		case "-v", "--version", "version":
			fmt.Println("cctree", version)
			return
		case "-h", "--help", "help":
			fmt.Println("cctree: browse every Claude Code session, running and past, as a folder tree.\nRun it with no arguments; keys are listed at the bottom of the screen.")
			return
		}
	}
	final, err := tea.NewProgram(NewModel(DefaultStore())).Run()
	if err != nil {
		fmt.Fprintln(os.Stderr, "cctree:", err)
		os.Exit(1)
	}
	if m, ok := final.(Model); ok && m.Resume != nil {
		resume(*m.Resume)
	}
}

// resume replaces cctree with `claude --resume <id>` in the session's folder.
func resume(s Session) {
	if err := os.Chdir(s.Cwd); err != nil {
		fmt.Fprintf(os.Stderr, "cctree: %s is gone; resuming from %s\n", s.Cwd, mustGetwd())
	}
	claude, err := exec.LookPath("claude")
	if err != nil {
		fmt.Fprintln(os.Stderr, "cctree: claude not found on PATH")
		os.Exit(1)
	}
	err = syscall.Exec(claude, []string{"claude", "--resume", s.ID}, os.Environ())
	fmt.Fprintln(os.Stderr, "cctree:", err)
	os.Exit(1)
}

func mustGetwd() string {
	wd, _ := os.Getwd()
	return wd
}
