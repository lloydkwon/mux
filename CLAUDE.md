# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## What this is

`mux` is a TUI tmux session manager (Go + Bubble Tea) that shows a live preview of every session, detects AI CLIs (`claude`, `codex`, `aider`, `gemini`), and displays git branch / worktree status and Claude token cost.

This repo is a **personal fork** of `lunemis/mux` — the module path is `github.com/xguru/mux`. Fork-specific features: the "New shell" / "New tmux session" action rows at the top of the list, and persistent per-session ordering (`0`–`9`, `o` to rotate sort).

## Commands

```bash
make build                       # → ./mux, injects version via -ldflags
make test                        # go test ./...
make install PREFIX=~/.local     # installs to $PREFIX/bin

go test ./ui -run TestFlattenMenu -v    # single test
go vet ./... && golangci-lint run       # expected before submitting (no repo config; defaults)
GOARCH=arm64 go build ./cmd/mux         # CI also cross-compiles this
```

CI (`.github/workflows/ci.yml`) runs test + build + arm64 cross-build. Releases are tag-driven via goreleaser. Commits follow Conventional Commits.

## Architecture

Three layers, strictly one-directional:

- `cmd/mux` — cobra CLI: root (TUI), `popup`, `setup-keybind`, `status`.
- `tmux/` — everything that shells out or reads the filesystem. No UI dependencies.
- `ui/` — Bubble Tea model, rendering, preferences. Depends on `tmux`.

### All shell exec goes through the injectable runner

`tmux/runner.go` defines a package-level `runner CommandRunner` var. Every `tmux`/`ps`/`pgrep` call in the package must use `runner.Output` / `runner.Run` — that is what makes the package testable. Tests call `SetRunner(mockRunner)` via the `withMock` helper in `tmux/runner_test.go`; the mock is keyed by the exact `"name arg1 arg2"` string, so adding a flag to a command breaks its test until the mock key is updated.

Two deliberate exceptions bypass the runner because they must hit the real process: `getTmuxVersion` in `tmux/popup.go`, and `AttachToSession`/`DetachClient` in `ui/app.go`.

### The 500ms tick drives everything — so everything expensive is TTL-cached

`ui/app.go` ticks every `refreshInterval` (500ms) and on each tick re-issues: `loadSessions`, a preview capture for the cursor row, token usage if the row is an AI session, plus `loadWindows`/`loadPanes` for *every* expanded subtree. Because of that fan-out, all cost sits behind mutex-guarded TTL caches:

| Cache | Location | TTL |
|---|---|---|
| resolved pane command (`pgrep`/`ps` scan) | `tmux/process.go` | 5s |
| git branch / worktree | `tmux/git.go` | 5s |
| Claude token usage (JSONL scan) | `tmux/claude.go` | 10s |

Anything new that runs per-tick needs the same treatment.

### Flattened tree list model

There is no nested view. `ui/tree.go`'s `flattenMenu` produces a flat `[]listItem` — action rows, then each session, then (if expanded) its windows, then (if expanded) their panes — and `Model.cursor` is an index into that slice. `treeState` holds the expansion sets plus the window/pane caches; expansion survives session refreshes, and `pruneCaches` drops entries for sessions that vanished.

Call `rebuildItems()` after any change to sessions, filter text, ordering, or expansion state, or the cursor will point at a stale row. Action rows are only included in the unfiltered top-level view.

### Preview targeting

`previewKey{session, window, pane}` uses `-1` as "the active one"; `.target()` renders it as tmux target syntax (`sess`, `sess:1`, `sess:1.2`). The model caches one preview blob plus the key it belongs to, and `viewMain` only renders it when that key still equals `previewKeyForItem(currentItem)` — otherwise the pane shows blank rather than the wrong session's output.

### Attaching happens after the TUI exits

Never attach from inside the Bubble Tea loop. `Update` records `attachTarget` / `detachRequested` and returns `tea.Quit`; `cmd/mux/main.go` inspects the returned model and then calls `ui.AttachToSession`, which `select-window`/`select-pane` first (attach replaces the process) and then either `switch-client` (inside tmux, `$TMUX` set) or `syscall.Exec` of `attach-session` (outside). "New shell" only detaches when `$TMUX` is set; outside tmux, quitting already returns to the login shell.

### Modal sub-models

`mode` selects which `updateX` handles input. Sub-models (`create`, `rename`, `filter`, `confirmKill`, `order`) are plain structs with their own `Update`/`View`; they report completion by emitting a message (`sessionCreatedMsg`, `sessionRenamedMsg`, `sessionOrderMsg`, …) that the *top-level* `Update` handles before mode dispatch — that is where mode is reset to `modeList` and side effects like preference writes happen.

### Manual fixed-size rendering

`ui/layout.go` (`padOrTruncate`, `fixedBox`, `drawBorder`, `joinHorizontalFixed`) does the layout by hand instead of using lipgloss containers, because `capture-pane -e` output carries raw ANSI that must be clipped to an exact cell width. Two consequences:

- Every rendered panel must return exactly `panelHeight` lines and exact widths, or the two columns desynchronize.
- **Width compensation is impossible here.** `drawBorder` re-pads every line to `innerWidth`, so any cell a row subtracts to account for a wide glyph is added straight back. The only workable rule is to emit glyphs whose drawn width equals `ansi.StringWidth` — verify a new glyph before using it (`TestGlyphWidthsAreStable` in `ui/list_test.go` pins the current set). Emoji measure and draw 2; most geometric shapes measure and draw 1.
- **Never wrap a row containing nested styled spans in an outer style.** A nested `lipgloss.Render` emits its own `ESC[0m`, which resets the *background* too, so a colored segment mid-row strips the selection highlight from everything after it. `renderRow` (`ui/layout.go`) builds rows from `rowSeg` values where each span re-states the full style, background included.

### Claude live-state chain

`tmux/claude_status.go` reads `~/.claude/sessions/*.json` — Claude Code's own state file, one per running session — and indexes them by tmux session name via each file's `tmux` field (`"<session>:@<win>.%<pane>"`). `ClaudeStatuses()` does one `os.ReadDir` per refresh behind a 1s TTL cache, and `ListSessions` hoists that single call out of its parse loop, so state arrives atomically with the session slice and needs no tick fan-out or `Model` cache.

Three things about this data are easy to get wrong:

- **The file is written only on status change, so mtime is not a heartbeat.** A crashed session leaves its file behind and would display as permanently busy. Liveness is `processAlive` (`tmux/proc.go`): signal-0 for existence, plus a best-effort `/proc/<pid>/stat` field-22 comparison against the file's `procStart` to catch PID reuse. The `/proc` half no-ops on macOS by design — no build tags.
- **`status: "waiting"` means blocked on the user**, whatever `waitingFor` says; a finished turn sitting at the prompt reports `idle` instead. That mapping lives in `mapClaudeState`.
- The format is private and unversioned. Every field decodes as optional; a renamed field must degrade to "no badge", never an error.

Because `Session.ActiveCommand` only reflects the *active pane of the active window*, this state file also covers Claude running in a background window. `tmux.SessionAITool` prefers it over `ActiveCommand` so the `✦` badge, token loading, and `mux status` all catch that case.

### Claude cost tracking chain

`tmux/claude.go`: pane PID → `pgrep -P` children → `~/.claude/sessions/<childPID>.json` → `{sessionId, cwd}` → `~/.claude/projects/<cwd with "/" replaced by "-">/<sessionId>.jsonl` → sum `usage` fields on `type: "assistant"` lines. Per-1M pricing constants are hardcoded in `estimateCost` (currently Opus-tier rates) and are an estimate only.

### Preferences

`ui/preferences.go` stores sort mode and the `{session name: order}` map in `os.UserConfigDir()/mux/preferences.json`, written atomically (temp file + `chmod 0600` + rename). `normalized()` is the validation gate — it runs on both load and save and drops blank names / non-positive orders. Rename and kill both fix up the `Orders` map so entries don't leak.

### tmux config editing

`SetupKeybind` (`tmux/popup.go`) writes an idempotent, marker-tagged (`# mux popup keybinding`) bind line. It detects gpakosz/.tmux ("oh-my-tmux") by symlink target or the `# : << 'EOF'` first-line signature, and for those installs writes to `.tmux.conf.local` *before* the `# "$@"` sentinel — writing into the main `.tmux.conf` corrupts oh-my-tmux's heredoc. It also strips legacy untagged installer binds. Preserve all of that when touching this file.
