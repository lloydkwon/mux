# Changelog

All notable changes to this project will be documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [Unreleased]

### Fixed
- `mux --version` reported `dev` for every build that did not go through the Makefile or goreleaser — most visibly `go install`, which is the documented install path. It now falls back to the module version Go records in the binary, then to the VCS revision (suffixed `-dirty` for an unclean tree), so an installed binary can be traced to a commit. An injected `-ldflags` value still wins, and every source is normalized to drop the leading `v` so the Makefile, goreleaser, and `go install` all print the same shape.

## [0.2.0] - 2026-08-09

### Added
- **Claude progress state** in the session list and preview: whether Claude is working, blocked waiting on you, or done and ready for input, with how long it has held that state. The preview also names the reason it is blocked.
- `tmux.ClaudeStatuses` reads Claude Code's own session state files (`~/.claude/sessions/*.json`) and indexes them by tmux session name, so state comes from the app's state machine rather than being inferred from pane output. One directory scan per refresh, TTL-cached, riding along on `ListSessions`.
- `tmux.SessionAITool` prefers live Claude state over `Session.ActiveCommand` when deciding which AI badge to show. `ActiveCommand` only reflects the active pane of the active window, so Claude running in a background window was previously invisible to the badge, the token display, and `mux status`.
- `processAlive` / `readProcStart` liveness check. Claude writes its state file only on status change, so a crashed session would otherwise linger as a permanently "working" row; pid existence plus a best-effort `/proc` start-time comparison retires stale entries. The start-time half no-ops where `/proc` is absent, so macOS needs no build tags.
- Login-friendly **New shell** and **New tmux session** action rows. New shell detaches the current client when selected from inside tmux.
- Persistent per-session Order values with multi-digit input (`0` clears).
- Sort rotation with `o`: recent activity, session name, and explicit Order.
- Multi-window/pane tree expansion in the session list (#14):
  - `Tab` / `→` / `l` to expand a session into its windows, then a window into its panes
  - `Shift+Tab` / `←` / `h` to collapse one level
  - Preview panel now follows the cursor — captures the targeted window or pane via `tmux capture-pane -t session:window.pane`
  - `Enter` on a window/pane row attaches and focuses that exact pane (`select-window` + `select-pane` before attach)
- `tmux.ListWindows` / `tmux.ListPanes` / `tmux.CapturePaneTarget` helpers
- MIT License
- English README with Korean translation (README.ko.md)
- CONTRIBUTING.md guide
- CODE_OF_CONDUCT.md (Contributor Covenant v2.1)
- Makefile with build, test, install, clean targets
- goreleaser configuration for automated releases
- VHS demo tape for recording demo GIFs
- Unit tests for tmux and UI packages
- `--version` flag
- `scripts/test-fixture.sh` for spinning up test sessions with multiple windows/panes

### Changed
- Module path renamed from `github.com/xguru/mux` to `github.com/lloydkwon/mux`. Install instructions in both READMEs and `install.sh` now point at this fork.
- Session list rows are built from styled segments (`renderRow`) instead of one preformatted string, and the state glyph plus elapsed time now sit in a fixed gutter ahead of the session name. Previously the time column was rightmost and was the first thing truncated on a narrow panel, losing its unit character. For a session running Claude that column shows the age of the *state* rather than of the session.
- `Session.Windows` (int) split into `Session.WindowCount` (int) + `Session.Windows` ([]Window) — the latter is lazily populated on demand
- `AttachToSession(name)` signature extended to `AttachToSession(name, windowIdx, paneIdx)` — pass `-1` to keep tmux defaults
- Cross-platform `shortenPath` using `os.UserHomeDir()` instead of hardcoded `/Users/`
- Go version in go.mod updated to stable release

### Fixed
- A colored segment in the middle of a list row emitted its own ANSI reset, which cleared the *background* as well, so everything after it lost the selected-row highlight — visible as the git branch dropping out of the highlight on a selected AI session row. Each segment now restates the full style.
- The preview header subtracted byte lengths from a cell budget. Every icon in it is multi-byte, so the AI badge and branch floated about five cells left of the right edge.
- Removed `labelInfo.extraWidth`. It claimed to compensate for ambiguous-width icons, but `drawBorder` re-pads every line to the panel width and adds the cell straight back, and the icons it targeted measure and draw one cell anyway. Glyph widths are now pinned by test instead.
- `renderPreview` test call missing `captured` parameter
- `setup-keybind` no longer corrupts `~/.tmux.conf` for [oh-my-tmux](https://github.com/gpakosz/.tmux) users (#15). Detects oh-my-tmux via symlink target or signature line, routes the bind line to `~/.tmux.conf.local` before the `# "$@"` sentinel, and cleans up any prior corrupt entry (including legacy untagged binds from older `install.sh`) from the main conf. `install.sh`'s shell fallback received the same treatment.

## [0.1.0] - 2026-03-30

### Added
- TUI session manager with list and live preview panels
- Real-time terminal output preview (500ms refresh)
- AI CLI detection (claude, codex, aider, gemini) with badge display
- Session create, delete, and rename from within the TUI
- Quick filter with `/` key
- Instant attach / switch-client
- Popup mode (`mux popup`) as tmux floating overlay
- `mux setup-keybind` for one-command tmux keybinding setup
- Cross-platform AI CLI detection (Linux/macOS)
