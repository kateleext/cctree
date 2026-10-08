# cctree

Browse every Claude Code session on your machine, running and past, as a folder tree.

`claude --resume` lists sessions as one long chronological list. cctree groups them by the folder each session started in, so you can walk your projects, see how many sessions each holds and which are running right now, open a session's subagents, and jump back into any of it.

```
Claude sessions  1240 sessions · 4 running

  ● running                               4 │ ~/work  12 here · 610 below
▾ ~                                  ●4 1240 │ ▸ api/                                ●2 214
  ▸ work                             ●4  622 │ ▸ web/                                  396
  ▸ notes                                 48 │ ● release checklist       ▸ 3 agents   idle
  ▸ scratch                              170 │ ○ migrate auth           ▾ 18 agents    2d
▸ /tmp                                   400 │   ↳ workflow wf_5136cc08 — 17 agents    2d
                                             │     ↳ review — correctness              2d
```
- **Left:** folders, each with its running (●) and total session counts. Folders with running sessions sort first; `● running` collects every live session.
- **Right:** the selected folder's own level (or, for `● running`, every running session at once) — its subfolders, then the sessions started in it. Sessions that spawned subagents expand to show them, nested by who spawned whom, with workflow runs grouped.
- **Preview:** the selected session's latest exchange — your last message and Claude's last reply — refreshed every few seconds while it runs.

## Install

macOS and Linux, with Homebrew:

```sh
brew install kateleext/tap/cctree
```

Or with Go 1.26+:

```sh
go install github.com/kateleext/cctree@latest
```

## Use

Run `cctree`.

| Key | |
| --- | --- |
| `↑` `↓` / `j` `k` | move |
| `←` `→` / `h` `l` | collapse / expand a folder; show or hide a session's subagents |
| `tab` | switch between the tree and the list |
| `enter` | open a folder; on a past session, quit and run `claude --resume` in its folder; on a running one, bring its terminal forward |
| `o` | resume a past session in a new terminal window |
| `/` | filter by title, first prompt, folder or id |
| `a` | running sessions only |
| `s` | show scripted sessions (headless `claude -p` and SDK runs), hidden by default |
| `r` | rescan |
| `q` | quit |

Running sessions refresh every three seconds. The first scan reads every transcript under `~/.claude/projects` (a few seconds for thousands of sessions); after that only changed files are read, from a cache in `~/.cache/cctree`.

Bringing a running session forward works on Hyprland (including sessions inside tmux). On macOS it selects the session's tmux pane when there is one; otherwise it tells you the pid and folder. New windows open with `xdg-terminal-exec` on Linux and Terminal.app on macOS.

`CLAUDE_CONFIG_DIR` is honoured if you keep Claude Code's data somewhere other than `~/.claude`.

## Release

```sh
scripts/release.sh v0.1.0
```

builds macOS and Linux archives, publishes the GitHub release and updates the formula in [kateleext/homebrew-tap](https://github.com/kateleext/homebrew-tap).
