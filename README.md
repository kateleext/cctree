# cctree

Browse every Claude Code session on your machine, running and past, as a folder tree.

`claude --resume` lists sessions as one long chronological list. cctree groups them by the folder each session started in, so you can walk your projects, see how many sessions each holds and which are running right now, open a session's subagents, and jump back into any of it.

```
── cctree · 1240 sessions · 4 running · 380 scripted hidden ──────────────────────
╭─ Running ─────────────── 4 ─╮╭─ Running now ─────────────────────────────── 4 ─╮
│ ▾ all                     4 ││ ── ~/work/api ─────────────────────────────── 2 │
│   ▾ ~/work                4 ││ ● release checklist          ▸ 3 agents   busy │
│       api                 2 ││ ● fix flaky auth test                     idle │
│       web                 2 ││                                                │
╰─────────────────────────────╯│ ── ~/work/web ─────────────────────────────── 2 │
╭─ Folders ───────────── 1240 ─╮│ ● landing page copy          ▸ 1 agent    idle │
│ ▾ ~                    1240 ││ ● migrate router            ▸ 18 agents   busy │
│   ▸ work                622 ││                                                │
│     notes                48 ││                                                │
╰─────────────────────────────╯╰────────────────────────────────────────────────╯
╭─ release checklist ───────────────────────────────────────────────── ● busy ─╮
│ ~/work/api · 7bfe14e7 · 3 agents                                             │
│ You     ship it once the changelog is in                                     │
│ Claude  Changelog added; tagging v2.3.0 now.                                 │
╰──────────────────────────────────────────────────────────────────────────────╯
```

- **Running:** a tree of just the folders with something running. It is selected when cctree opens, so every running session is listed straight away, grouped by folder.
- **Folders:** every folder with its session count. The list on the right shows the selected folder's own level: its subfolders, then the sessions started in it.
- **Subagents:** sessions that spawned subagents expand to show them, nested by who spawned whom, with workflow runs grouped.
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
