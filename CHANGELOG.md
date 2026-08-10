# Changelog

All notable changes to this project will be documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [Unreleased]

### Added
- `?` opens a full-screen help page. The list packs a lot into a few cells — `▶`/`▼`, `*`/`○`, `#N`, an elapsed column that silently switches from session age to AI-state age, `⌥`/`⌥⌥`, and the AI badge — and none of it was explained anywhere but the README. The page is a marker legend first, key table second, written in Korean. It reads the tool icons and their colors out of `tmux.LookupAITool` and the state glyphs out of `AIState.Icon()`, so the legend cannot drift from what the list actually draws. It renders through `fixedBox` rather than the centered overlay the other modals use: a `mux popup` on an 80x24 terminal gets 68x19, which a centered box would overflow, and a test pins the body to that budget.

### Changed
- The footer bar gained a `? help` entry, and its separator tightened from `  •  ` to ` • `. The bar is clipped at the terminal's width rather than wrapped, so at twelve entries the roomier separator pushed `q quit` off the end of a 150-column screen.
- Claude's live state is no longer a feature beside AI CLI detection — it is now part of it. The list row's dedicated state column is gone; the existing AI badge shows the state glyph (`⏳`/`❗`/`✅`) in place of the tool icon when a tool publishes state, and keeps its own icon (`✦ ◈ ⬡ ✧`) when it does not. Previously a Claude session rendered both, telling you the same thing twice.
- `Session.ClaudeState` / `ClaudeWaitingFor` / `ClaudeSince` → `Session.AIState` / `AIWaitingFor` / `AISince`, with `tmux.ClaudeState` → `tmux.AIState` moved into `tmux/aitools.go` next to the tool registry. `tmux/claude_status.go` stays the Claude-specific *reader* — it is the only producer of a non-zero `AIState` — but nothing downstream speaks Claude any more. `ui/status.go`'s render helpers renamed to match (`aiGlyph`, `aiStateColor`, `aiBadgeColor`, `aiStatusText`).
- `mux status` applies the same badge rule, so the tmux status bar shows which session is blocked rather than just which sessions run an AI CLI.
- The badge cell is padded to a fixed width and always emitted, so the git branch now lines up across every row — previously rows without a badge shifted it left.

### Fixed
- Token-cost loading fired every 500ms for any detected AI CLI, but it only ever reads Claude's session logs — a codex/aider/gemini session under the cursor spent a `pgrep` plus a directory scan per tick to find nothing. It is now gated on Claude.

## [0.2.1] - 2026-08-09

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
