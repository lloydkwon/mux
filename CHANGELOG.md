# Changelog

All notable changes to this project will be documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [Unreleased]

### Added
- The panel remembers the width you dragged it to, per session, and reopens at it — and holds it against tmux. Stored as a tmux user option rather than in `preferences.json`: `mux watch` is a separate process from the TUI, which holds its preferences in memory from startup and writes the whole file back, so a width saved by the panel would be silently clobbered — and a tmux option needs no cleanup on rename or kill, since it dies with the session exactly as the panel does. The width is applied at split time rather than by resizing afterwards, so the pane never appears at the wrong size first. Resizing itself already worked: a border drag is handled by tmux, not forwarded to the pane, so it never collided with the mouse reporting that makes rows clickable.
- `mux panel`, which closes the panel pane in a tmux window if there is one and opens it otherwise. The binding used to be a bare `split-window`, so it could only open — pressing it twice left two panels stacked in one window, and hiding the panel meant killing its pane by hand. Because a freshly created window cannot already hold a panel, the keybinding and the `after-new-window` / `after-new-session` hooks all call this one command instead of needing an open-only variant, which is what makes "show it everywhere by default" a two-line config. It finds the panel by the `mux watch` in tmux's recorded `pane_start_command` and resolves its own path with `os.Executable()`, so the config no longer hardcodes where the binary lives.
- Clicking a session row in the `mux watch` panel switches to that session. Enabling mouse reporting costs that pane tmux's own wheel-scroll and drag-select, which Shift still bypasses at the terminal level. Rows carry their owning session through rendering rather than having the click handler re-walk the layout: an approval row is followed by an extra reason line and each session by a blank that clicks to the same session, so a second copy of that loop would silently pick the wrong one. Switching hands focus back to the pane you were working in first — tmux makes the panel active to deliver the click, and the window left behind would otherwise report the panel's command and directory as its session's own.
- `mux watch`, a long-running mode that renders the notification panel to fill a dedicated tmux pane, so AI session state is visible while you work instead of only while the TUI is open. It has to be a pane: tmux has no floating window that leaves the keyboard alone — `display-popup` is documented as *"Panes are not updated while a popup is present"*, freezing everything behind it and capturing input, which is exactly what a glanceable overlay must not do. That constraint is why the companion `my-mux` widget reaches for a browser Picture-in-Picture window; inside tmux a real pane is the only always-visible surface that still lets you type. It ticks at 2s rather than the TUI's 500ms — that rate exists to keep the cursor row's preview live, and this is a second process with no cursor. It keeps its own transition history, since a separate process cannot share the TUI's.
- `?` opens a full-screen help page. The list packs a lot into a few cells — `▶`/`▼`, `*`/`○`, `#N`, an elapsed column that silently switches from session age to AI-state age, `⌥`/`⌥⌥`, and the AI badge — and none of it was explained anywhere but the README. The page is a marker legend first, key table second, written in Korean. It reads the tool icons and their colors out of `tmux.LookupAITool` and the state glyphs out of `AIState.Icon()`, so the legend cannot drift from what the list actually draws. It renders through `fixedBox` rather than the centered overlay the other modals use: a `mux popup` on an 80x24 terminal gets 68x19, which a centered box would overflow, and a test pins the body to that budget.

### Changed
- `mux panel --auto`, which the hooks now use, stands down where the panel costs more than it is worth: a window narrower than 96 columns, and a session only being viewed from a VS Code integrated terminal — the panel's 48 columns cost more there than they are worth. The keybinding passes no flag and still opens it, since pressing a key is a decision rather than a default. tmux has no per-client pane visibility, so "hide it in VS Code" can only mean "do not create it": a window open in both terminals shows the panel to both. A session with no client attached is not treated as VS Code — no evidence is not evidence, and defaulting the other way would quietly deny a panel to every session a script creates. The width rule is how "not on a phone" is decided, since a mobile SSH client has no environment marker to match on and the real problem was never the device: with `aggressive-resize`, attaching from a phone shrank the window to 54 columns while the panel held its 48 and left the work pane five. A panel already open when that happens now closes itself, and `prefix+a` brings it back on a wide screen.
- The notification panel no longer floats over the TUI's preview; `mux watch` is its only home. The overlay predated the dedicated pane, and once the pane was on screen in every window the TUI drew the same thing a second time, over the preview it was covering. `overlayTopRight` went with it — nothing else composites one block onto another.
- The default panel width is 48 columns, up from 40. A session with a remembered width still opens at that.
- The panel orders sessions by the elapsed time each row prints, most recent first. It used to inherit `ListSessions()`'s attached-first-then-activity order, which made the column non-monotonic — a session at 41m sitting under two at 3h reads as a bug.
- The panel marks a linked git worktree with `⌥⌥`, matching the preview and the glyph legend in the help page — it printed a plain `⌥` for both before, which made the legend wrong. The choice now lives in one `branchGlyph`, for the same reason `aiGlyph` is a single decider: it had already drifted three ways across the preview, the list, and the panel.
- The footer bar gained a `? help` entry, and its separator tightened from `  •  ` to ` • `. The bar is clipped at the terminal's width rather than wrapped, so at twelve entries the roomier separator pushed `q quit` off the end of a 150-column screen.
- Claude's live state is no longer a feature beside AI CLI detection — it is now part of it. The list row's dedicated state column is gone; the existing AI badge shows the state glyph (`⏳`/`❗`/`✅`) in place of the tool icon when a tool publishes state, and keeps its own icon (`✦ ◈ ⬡ ✧`) when it does not. Previously a Claude session rendered both, telling you the same thing twice.
- `Session.ClaudeState` / `ClaudeWaitingFor` / `ClaudeSince` → `Session.AIState` / `AIWaitingFor` / `AISince`, with `tmux.ClaudeState` → `tmux.AIState` moved into `tmux/aitools.go` next to the tool registry. `tmux/claude_status.go` stays the Claude-specific *reader* — it is the only producer of a non-zero `AIState` — but nothing downstream speaks Claude any more. `ui/status.go`'s render helpers renamed to match (`aiGlyph`, `aiStateColor`, `aiBadgeColor`, `aiStatusText`).
- `mux status` applies the same badge rule, so the tmux status bar shows which session is blocked rather than just which sessions run an AI CLI.
- The badge cell is padded to a fixed width and always emitted, so the git branch now lines up across every row — previously rows without a badge shifted it left.

### Fixed
- The panel drifted wider every time you switched sessions, and ended up a different width in each one. With `aggressive-resize on` a switch resizes windows, tmux redistributes the panes proportionally, and the panel took every one of those resizes for a deliberate one — so the drift was saved as if you had dragged it. `applyResize` now separates the two by reading the *window* width: an unchanged window with a changed pane is a border drag and is kept, a changed window is a re-layout and is undone. Two rules keep that from running away, both learned the hard way — the window width is read synchronously, because fetching it in a `tea.Cmd` let a stale value read as "window unchanged" and a momentarily squeezed pane became the width being enforced, collapsing the panel to one column; and a width below the panel's minimum is never taken as intent.
- Every tmux call from `mux watch` now names its own pane. tmux resolves an omitted target to the window's *active* pane, which is the one the user works in — the panel is created detached and never becomes active on its own. A width correction sent that way shrank the wrong pane, and the panel grew to fill what it gave up.
- A session that left a dev server running never reported a finished turn, so the notification panel's event log stayed empty for it forever. Claude sets `status: "shell"` whenever any background shell is alive and that outranks both `idle` and `busy` in its own precedence — measured, a session actively generating output reported `shell` for 13 minutes because one server was up. When every Claude-owned shell job under a session is a long-running server (`yarn dev`, `gradlew bootRun`, `vite`, `uvicorn`, …) the turn really is over, so the state is promoted to ready. A build, a test run or a download is real work and the shell state stands; so does "nothing found", since a system without `/proc` must not read as "the turn ended". Ported from my-mux's `demoteServerShells`, and applied where the state is produced rather than in one consumer, so the TUI, `mux watch`, `mux status` and `--json` all agree.
- `switch-client`/`attach-session` targeted sessions without the `=` exact-match prefix, so tmux fell back to prefix and then glob matching — attaching to `mux` was ambiguous next to `mux-old`, and a name containing `*` or `[` could reach a different session entirely.
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
