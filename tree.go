package main

import (
	"sort"
	"strings"
	"time"
)

type Folder struct {
	Path     string
	Name     string
	Folders  []*Folder
	Sessions []Session // sessions whose cwd is exactly this folder
	Total    int
	Live     int
	Latest   time.Time
}

// All returns every session in the folder and below it: running first,
// then newest first.
func (f *Folder) All() []Session {
	var out []Session
	var walk func(*Folder)
	walk = func(n *Folder) {
		out = append(out, n.Sessions...)
		for _, c := range n.Folders {
			walk(c)
		}
	}
	walk(f)
	sortSessions(out)
	return out
}

// BuildTree groups sessions by working directory, folding chains of
// single-child folders into one ("work/apps").
func BuildTree(sessions []Session, homeDir string) *Folder {
	root := &Folder{}
	for _, s := range sessions {
		shown := DisplayPath(s.Cwd, homeDir)
		parts := strings.Split(strings.Trim(shown, "/"), "/")
		if strings.HasPrefix(shown, "/") {
			parts[0] = "/" + parts[0]
		}
		node := root
		for _, part := range parts {
			if part == "" {
				continue
			}
			path := part
			if node.Path != "" {
				path = node.Path + "/" + part
			}
			node = node.child(path, part)
		}
		node.Sessions = append(node.Sessions, s)
	}
	root.finish(true)
	return root
}

func DisplayPath(cwd, homeDir string) string {
	if homeDir != "" && (cwd == homeDir || strings.HasPrefix(cwd, homeDir+"/")) {
		return "~" + strings.TrimPrefix(cwd, homeDir)
	}
	return cwd
}

// Private

func (f *Folder) child(path, name string) *Folder {
	for _, c := range f.Folders {
		if c.Path == path {
			return c
		}
	}
	c := &Folder{Path: path, Name: name}
	f.Folders = append(f.Folders, c)
	return c
}

func (f *Folder) finish(isRoot bool) {
	for _, c := range f.Folders {
		c.finish(false)
	}
	for !isRoot && len(f.Sessions) == 0 && len(f.Folders) == 1 {
		only := f.Folders[0]
		f.Name += "/" + only.Name
		f.Path, f.Sessions, f.Folders = only.Path, only.Sessions, only.Folders
	}
	sortSessions(f.Sessions)
	f.Total, f.Live, f.Latest = len(f.Sessions), 0, time.Time{}
	for _, s := range f.Sessions {
		if s.Live {
			f.Live++
		}
		if s.Updated.After(f.Latest) {
			f.Latest = s.Updated
		}
	}
	for _, c := range f.Folders {
		f.Total += c.Total
		f.Live += c.Live
		if c.Latest.After(f.Latest) {
			f.Latest = c.Latest
		}
	}
	sort.SliceStable(f.Folders, func(i, j int) bool {
		a, b := f.Folders[i], f.Folders[j]
		if (a.Live > 0) != (b.Live > 0) {
			return a.Live > 0
		}
		return a.Latest.After(b.Latest)
	})
}

// Helpers

func sortSessions(list []Session) {
	sort.SliceStable(list, func(i, j int) bool {
		if list[i].Live != list[j].Live {
			return list[i].Live
		}
		return list[i].Updated.After(list[j].Updated)
	})
}
