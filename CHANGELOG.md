# Changelog

All notable changes to this project will be documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [Unreleased]

### Added
- The panel restarts itself when mux is installed over. `mux watch` is one
  process per pane and lives for days, so replacing the binary left every open
  panel running the code it had loaded — a fix you had just installed visibly
  did not take, and nothing said why. There is no window event after an install
  for the panel hooks to notice, so the panel now watches its own path and
  `exec`s into the new file: same pane, same width, same focus, and the same PID.
  A change has to be seen twice before it acts — `install` cannot write over a
  running binary, so it unlinks and writes a new one, and the path names a
  half-copied file for as long as that takes. Measured end to end: 4.5 seconds
  from `install` to the new binary running, and a failed exec leaves the panel
  up on the code it already had rather than closing the pane.

## [0.3.7] - 2026-09-01

### Changed
- A finished turn says how long it took when there is no name to print
  (`✅ 작업 완료 · 3m 12s`). The name a transition prints now comes only from a
  session Claude named itself: the state file's `nameSource` distinguishes the
  tool's own naming (`auto`, `derived`, or missing on older builds) from a name
  the user set by hand, and a hand-set name does not move — two renamed sessions
  here carried the same label for a day while Claude renamed one of its own
  three times in an afternoon. Repeating a fixed label on every transition is
  what naming the work was meant to stop, so those sessions get the duration.

### Fixed
- The blank line between sessions in the panel is back. It was removed in 0.3.6
  on the grounds that the new per-session event line already made a click land
  on a block rather than a single row — which it does, but the blank's other job
  turned out to be the load-bearing one: without it seven two-line blocks run
  together into a wall, on a panel whose whole point is being read at a glance.

## [0.3.6] - 2026-09-01

### Added
- `make tmux-check` (`scripts/tmux-assumptions.sh`) verifies what mux assumes
  about tmux against the tmux actually installed, and CI runs it too. Every unit
  test mocks the runner — that is what keeps them hermetic, and it also means
  tmux can change underneath mux without a single test failing. The script
  starts a private server (`-L`, `-f /dev/null`, so your own sessions and hooks
  are never touched) and checks 22 behaviours the code leans on: the version
  string `getTmuxVersion` parses, the session-list field count, user options
  resolving through a pane target, `split-window -l N` giving exactly N columns,
  `list-panes` remembering the start command the panel is found by, batched
  captures emitting one separator, and `run-shell` and `after-new-session`
  expanding `#{pane_id}`. `TMUX_BIN=/path/to/tmux` aims it at a candidate
  build instead of PATH, which is the point: a new tmux on PATH makes the client
  new while the running server stays old, and the protocol mismatch rejects every
  tmux call until the server restarts — taking every session with it. Checked
  this way against 3.4 and 3.7c: 22/22 on both.

### Changed
- A logged transition now names the work instead of restating the state. The
  glyph already says working or done; the words carry Claude's own name for the
  turn — `⏳ panel-session-last-notification` rather than a second `⏳ 작업 중`.
  It comes from the `name` field of Claude's state file, which every live
  session observed carried. Tools that publish no task keep the label.
- The panel now shows every session's last event on a line under its row, with
  the chronological log of everything below the list. The badge says what a
  session is doing *now*, not what it just finished, and reading that off a flat
  log meant scanning past whichever session was busiest.
- The shared log keeps the newest entry of every session the 50-entry cut would
  otherwise silence, so a busy session can no longer evict a quiet one entirely.
  Measured before this: fifty entries covering sixty-four minutes, twenty of them
  one session's, and two of seven running sessions with nothing in the log at
  all. Entries for sessions that no longer exist are dropped — nothing draws
  them, and they were holding 12% of the log.

### Fixed
- The list sorted by a clock it does not show. "Recent" ordered sessions by
  tmux's `session_activity` while the elapsed column beside it printed the AI
  state's age, so a correctly sorted list read out of order — measured on a live
  server, the sort key ran 40s → 53s → 1m → 52m while the column read 39s, 1m,
  28h, 5m. It now sorts by the value the row prints, the way the panel already
  did. That is also the more useful clock here: `session_activity` says a pane
  drew something, which a session left running does whether or not anything
  happened in it.
- The session list no longer opens filtered when you summon it from a real
  terminal. Narrowing to the session's project was keyed off the `@project_dir`
  tag alone, so it applied wherever the session was viewed from — including
  Windows Terminal, where the session manager is opened precisely to see
  everything, and where the first keystroke became `esc`. The client looking at
  the list now decides, and only a VS Code integrated terminal narrows it. Both
  facts come from one `display-message`, so startup costs no more than before.

## [0.3.5] - 2026-08-31

### Fixed
- `mux setup-panel` no longer takes Alt+Enter. It bound the panel's commit key as
  `bind -n M-Enter`, and a rootless binding takes that key from every program in
  every pane — Alt+Enter is how Claude Code inserts a newline, so mux was
  breaking the tool it exists to sit beside. Committing now sits behind the
  prefix (`prefix + Enter`); moving the cursor stays rootless on `M-Up`/`M-Down`,
  where one keystroke is the whole point and nothing collides. Existing configs
  keep the old line until `mux setup` is run again — `tmux unbind -n M-Enter`
  frees the key immediately.

## [0.3.4] - 2026-08-31

### Added
- `mux switch <session>` points the Windows Terminal tmux client at a session, so
  a widget outside tmux can drive it. Only Windows Terminal clients are moved — a
  VS Code integrated terminal watches one session on purpose, and dragging it
  elsewhere would be hostile. Where there is no such client, a running TUI is
  asked to attach instead, over a unix socket on a fixed path (the environment
  a `wsl.exe` invocation gets has no `XDG_RUNTIME_DIR` to agree on).
- `mux status --json --watch` keeps running and prints a JSON line whenever the
  marshalled output changes, so a widget can subscribe rather than spawn a
  process per poll.
- `vscodeDir` in that JSON: the workspace folder of a VS Code window whose
  integrated terminal is attached to the session. Counted only when the
  session's own directory is inside that folder — a terminal that attached
  merely to peek is not that window's project.
- The session list opens filtered to the project the session belongs to, read
  from a `@project_dir` tmux option. tmux sessions are server-wide, so every
  editor window used to list every session regardless of what it was open on.
  `esc` clears the filter as always, and a session carrying no tag behaves
  exactly as before.

### Changed
- The panel is one width, everywhere. It was also remembered per session in
  `@mux_panel_width`, and that option outranked the saved copy — so dragging a
  border in one session left every other panel where it was, which is not what
  one mouse and one number should do. The option is gone; the width lives in
  `~/.config/mux/panel.json` alone, and every open panel follows it on its next
  tick. A panel in a narrow window still clamps to half its window, but never
  writes the clamped value back: one cramped window must not drag the rest down
  to its size.

### Fixed
- The panel no longer lands in sessions opened for a VS Code window.
  `SessionOnlyInVSCode` has to inspect an attached client, and
  `after-new-session` fires before any client attaches — so it answered "not VS
  Code" and the panel went in, permanently, since the hook path only ever opens.
  The `@project_dir` tag is there from the moment the session exists and needs
  no client to read. Pressing the key still opens one.

## [0.3.3] - 2026-08-30

### Added
- First-run onboarding: launched with a tmux config that carries no mux region,
  the TUI now offers to install the integration — popup bind, panel bind and
  hooks, default keys — and applies it to the running server on `y`. Asked
  once, ever; declining leaves the new `mux setup` command, which bundles
  `setup-keybind` and `setup-panel` for the command line.

### Fixed
- The ghost-pane cleanup no longer kills a window's last pane. tmux-resurrect
  restores the panel's title onto the shell it leaves in the panel's place, and
  a user who kept working in that shell owned a pane named exactly like the
  panel — reopening the panel then killed it, and with it the session. A ghost
  that is all the window has now loses its title instead of its life, and the
  real panel opens beside it.

## [0.3.2] - 2026-08-29

### Added
- The panel logs a session entering *working*, which my-mux's history has always
  done and this had not. A turn now shows both ends, so the time it took is
  readable off the list rather than only the moment it finished. It halves how
  far back the fifty-entry log reaches, which is the trade it makes.
- The panel reports a second approval prompt arriving while a session is already
  blocked. Claude stamps each one, but the detector only compared states, so
  answering one prompt and being asked another looked like no change at all —
  silence on exactly the transition the panel exists to report. It also now logs
  a session's disappearance (`○ 종료`), the one transition whose badge goes away
  with it.

### Changed
- The panel says what a blocked session is waiting for in its own language:
  `input needed` → `입력 대기`, `permission prompt` → `권한 승인`. Anything else
  Claude puts in that field is the command it wants to run, and passes through
  verbatim. `mux status` and the TUI keep the English, which is what they print
  everywhere else.
- The panel opens on the **right** of the window (`split-window -h`), reversing
  0.3.0's move to the left. "A glance goes left before it goes right" holds on a
  monitor and not on a phone: a client narrower than the window shows the
  leading columns, so a panel on the left is the half you can see and the
  session you came to read is the half you cannot. The pane you type in gets the
  left edge. An open panel does not move itself — close it and let the hooks
  reopen it.

### Fixed
- The panel adopted, saved and then enforced a width tmux had handed it when a
  neighbouring pane closed. In a two-pane window the freed columns land on
  exactly half, which the "no wider than half" ceiling let through — observed
  at 118 of 237 columns, written to `panel.json`, and held there. The resize
  check now reads the window's pane count alongside its width and treats a
  changed count as a re-layout rather than a drag. A deliberate drag to exactly
  half is still yours to make.
- A mux installed at a path containing a space silently disabled `prefix + m`,
  the panel bindings, the nav bindings and every panel hook. The generated lines
  put the path into a shell command without quoting it, so `/bin/sh` split it in
  two and the command never ran — the only symptom being a line on the status
  bar. All of them now carry the shell-level quoting the border line already
  had. The hooks became `{ }` blocks in the process, since tmux has no escape
  inside single quotes and the quoted form is otherwise a parse error.
- mux now repairs its own regions of the tmux config when they name an older
  copy of itself — after installing to a new location, tmux kept calling the
  binary you thought you had replaced, and deleting the old one turned every
  window event into a status-line error. Keys are carried over, hand-edited
  regions are left alone, and the running server picks the change up without a
  config reload.
- `install.sh` reported success whenever *some* `mux` was on PATH after
  `go install`, which an older copy earlier on PATH satisfies just as well. It
  now compares what it installed against what typing `mux` actually runs, and
  says which one wins.
- Quitting the popup that a bare `mux` opens outside tmux left you attached to
  whichever session `attach-session` picked, rather than back in the terminal
  you typed in — `q` closed the popup and dropped you inside tmux, with no way
  out short of knowing the detach key. The bootstrap now marks its popup
  (`MUX_BOOTSTRAP_POPUP`) and quitting one detaches the client it attached.
  Choosing a session is unaffected, and `prefix + m` never attached anything so
  it is untouched.

## [0.3.1] - 2026-08-14

### Added
- The session's summary now sits on the border above the pane you work in —
  name, directory, AI tool and its live state, git branch — so it is on screen
  without opening the TUI over the top of what you were doing. `mux setup-panel`
  turns on tmux's `pane-border-status` and points `pane-border-format` at a new
  `mux border` command. It has to be the border: the pane below is your shell,
  and its title already belongs to whatever runs in it. The format skips the
  panel's own pane, so the summary appears beside the list rather than above it,
  and the line gives up its branch, then the tool's name, then the directory as
  the pane narrows — never the session name. It costs one row per pane.

## [0.3.0] - 2026-08-14

The release the sidebar arrived in. `mux` used to be a thing you opened, looked
at, and closed; `mux watch` makes session state something that is simply on
screen while you work, and the colours it draws in are now the ones your terminal
was configured with rather than a palette picked against a dark background.

### Added
- `mux watch`, a long-running mode that renders the notification panel to fill a dedicated tmux pane, so AI session state is visible while you work instead of only while the TUI is open. It has to be a pane: tmux has no floating window that leaves the keyboard alone — `display-popup` is documented as *"Panes are not updated while a popup is present"*, freezing everything behind it and capturing input, which is exactly what a glanceable overlay must not do. That constraint is why the companion `my-mux` widget lives on the desktop as a GTK3 dock window instead; inside tmux a real pane is the only always-visible surface that still lets you type. It ticks at 2s rather than the TUI's 500ms — that rate exists to keep the cursor row's preview live, and this is a second process with no cursor. It keeps its own transition history, since a separate process cannot share the TUI's.
- `mux panel`, which closes the panel pane in a tmux window if there is one and opens it otherwise. The binding used to be a bare `split-window`, so it could only open — pressing it twice left two panels stacked in one window, and hiding the panel meant killing its pane by hand. Because a freshly created window cannot already hold a panel, the keybinding and the `after-new-window` / `after-new-session` hooks all call this one command instead of needing an open-only variant, which is what makes "show it everywhere by default" a two-line config. It finds the panel by the `mux watch` in tmux's recorded `pane_start_command` and resolves its own path with `os.Executable()`, so the config no longer hardcodes where the binary lives.
- `mux setup-panel` installs the panel keybinding and the tmux hooks that keep a
  panel in every window you end up looking at. The panel is a pane and a pane
  belongs to one window, so switching sessions from the panel landed you in a
  window that had never had one — the panel appearing to vanish. Seven hooks now
  cover every way a panel-less window can reach the screen, `client-session-changed`
  being the one that answers that case. It writes a fenced `# mux panel { … }`
  block with the absolute path filled in, replaces that block on re-runs rather
  than stacking copies, routes to `.tmux.conf.local` before the sentinel for
  oh-my-tmux, and sits above a trailing tpm loader, which tpm requires to be the
  last line.
- `mux nav <up|down|top|bottom|enter>` steers the panel from a tmux binding. It
  reaches the panel with `send-keys`, so the cursor moves without the focus
  leaving your own pane — focusing the panel would take the keyboard away from
  the pane you are working in. A window with no panel exits cleanly, so the
  binding is safe to make global.
- `prefix + Tab` steps the focus into the panel and back out again, and `esc`
  leaves it without choosing. The panel now has two keyboards on purpose: this
  one for when you want to browse it, and `mux nav` for when you do not want to
  give up your cursor at all. Stepping back out is `select-pane -l`, so it
  returns to the pane you came from however many sit beside the panel.
- Clicking a session row in the `mux watch` panel switches to that session. Enabling mouse reporting costs that pane tmux's own wheel-scroll and drag-select, which Shift still bypasses at the terminal level. Rows carry their owning session through rendering rather than having the click handler re-walk the layout: an approval row is followed by an extra reason line and each session by a blank that clicks to the same session, so a second copy of that loop would silently pick the wrong one. Switching hands focus back to the pane you were working in first — tmux makes the panel active to deliver the click, and the window left behind would otherwise report the panel's command and directory as its session's own.
- The TUI answers the mouse at all now: a click selects a row and previews it, a
  double click or `enter` attaches. Both go through the same `activateCurrent()`
  the key uses, since a click that attached somewhere other than where `enter`
  would is a bug nobody would think to look for. The cost is the terminal's own
  scroll and drag-select in that window, which Shift still gets.
- The panel lists sessions running no AI CLI too, in a second group below the
  ones that do, so every session is reachable without opening the TUI.
- The session the panel itself sits in is marked `◀`, and the cursor opens on it.
  There is one panel per window, so switching sessions puts you in front of a
  panel that has never been told anything — opening anywhere else meant a sidebar
  highlighting a session you had not asked about. Pressing enter there is not a
  no-op either: it hands you back to the pane you were working in.
- The panel remembers the width you dragged it to across a tmux server restart,
  not just for the life of the session (`~/.config/mux/panel.json`). The tmux
  user option that held it dies with the server, which is exactly when a
  remembered width is worth most.
- `mux new` opens straight on the create-session prompt and attaches to what it
  creates.

- The panel remembers the width you dragged it to, per session, and reopens at it — and holds it against tmux. Stored as a tmux user option rather than in `preferences.json`: `mux watch` is a separate process from the TUI, which holds its preferences in memory from startup and writes the whole file back, so a width saved by the panel would be silently clobbered — and a tmux option needs no cleanup on rename or kill, since it dies with the session exactly as the panel does. The width is applied at split time rather than by resizing afterwards, so the pane never appears at the wrong size first. Resizing itself already worked: a border drag is handled by tmux, not forwarded to the pane, so it never collided with the mouse reporting that makes rows clickable.
- `?` opens a full-screen help page. The list packs a lot into a few cells — `▶`/`▼`, `#N`, an elapsed column that silently switches from session age to AI-state age, `⌥`/`⌥⌥`, and the AI badge — and none of it was explained anywhere but the README. The page is a marker legend first, key table second, written in Korean. It reads the tool icons and their colors out of `tmux.LookupAITool` and the state glyphs out of `AIState.Icon()`, so the legend cannot drift from what the list actually draws. It renders through `fixedBox` rather than the centered overlay the other modals use: a `mux popup` on an 80x24 terminal gets 68x19, which a centered box would overflow, and a test pins the body to that budget.

### Changed
- **Colours come from the terminal's palette.** `ui/styles.go` names roles — accent, muted, danger — and fills them with ANSI palette indices rather than hex, so a light scheme gets colours its author chose for a light background; ordinary row text sets no colour at all and takes the terminal's own foreground. It was hex before, all of it picked against a dark terminal: measured on GitHub Light, the colour every list row was drawn in reached 2.4:1 contrast and the accent 1.7:1, against a 4.5:1 floor. `lipgloss.AdaptiveColor` was rejected on purpose — it asks the terminal for its background over OSC 11, and `mux watch` runs in a tmux pane where the answer may never arrive, which would draw the panel in the opposite palette from the TUI beside it. A selected row is reverse video, and segment colours are dropped on it: under reverse a colour set on a span paints as its *background*, so the row would grow green and red blocks.
- **The list reads as a table.** The gutter went from twelve cells to two: the attached session became its name in the accent colour rather than a marker column, and the `#3` order column appears only when some session in view carries an order. The name leads, then age, badge and branch in columns decided once per render — a column sized per screenful would move the names as you scrolled — and the branch is flush right and the first thing to yield when a row runs out of room. Both columns now sit inside **one** frame with a `┬ │ ┴` divider instead of two boxes meeting in a seam.
- The panel opens on the **left** of the window (`split-window -hb`) rather than
  the right. A glance goes left before it goes right, and the panel is the thing
  you glance at.
- The panel names live states in Korean, the way its event log always has,
  rather than through `AIState.String()` — that one is the English the TUI and
  `mux status` print. `aiStateLabel` is now the panel's single decider.
- The panel opens at 36 columns by default, down from 48 and up from 40 before
  that. It is the narrowest width that still answers the panel's questions —
  below about 32 the branch column goes and event text starts being cut — rather
  than the narrowest it survives, which is much lower, because someone dragging
  the border deliberately gets to choose.

- `mux panel --auto`, which the hooks use, is an ensure rather than a toggle — it opens a missing panel and never closes one, so the resize hooks can call it without the panel flapping. It stands down where the panel costs more than it is worth: a window narrower than 140 columns, a session only being viewed from a VS Code integrated terminal, and a window where the panel was closed by hand (recorded as `@mux_panel_off`, or any stray resize would undo the key and the toggle would stop meaning anything). The keybinding passes no flag and still opens it, since pressing a key is a decision rather than a default. tmux has no per-client pane visibility, so "hide it in VS Code" can only mean "do not create it": a window open in both terminals shows the panel to both. A session with no client attached is not treated as VS Code — no evidence is not evidence, and defaulting the other way would quietly deny a panel to every session a script creates. The width rule is how "not on a small screen" is decided, since a mobile SSH client has no environment marker to match on and the real problem was never the device: with `aggressive-resize`, attaching from a phone shrank the window to 54 columns while the panel held its width and left the work pane five. A panel already open when that happens closes itself, and the `client-resized` / `after-resize-window` hooks bring it back when the window grows again. `@mux_panel_min_width` moves the bar for a screen it was not measured on.
- The notification panel no longer floats over the TUI's preview; `mux watch` is its only home. The overlay predated the dedicated pane, and once the pane was on screen in every window the TUI drew the same thing a second time, over the preview it was covering. `overlayTopRight` went with it — nothing else composites one block onto another.
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
- The panel could grow until it owned the window — measured at 188 of 237
  columns, leaving 48 to work in, and it stuck: the width was saved to the
  session and then actively held. `applyResize` reads "same window width,
  different pane width" as a border drag, which is also what a pane appearing
  or dying beside the panel looks like. It is now capped at half its window,
  put back when something pushes it past that, and capped on the first size
  seen too, so a width remembered from a roomier window heals rather than
  persisting.
- `select-pane -l` fired when the panel was not the focused pane, which moved
  the user off the pane they were typing in. It is only ever correct after a
  click — tmux makes a pane active before forwarding one — and is now gated on
  `PaneActive`.
- Every tmux failure surfaced as `exit status 1`. Whatever tmux said about it is
  carried with the error now, which is how "duplicate session: mux" had been
  invisible.
- The panel silently refused to open in windows under 200 columns, which
  included an ordinary 188-column terminal anyone would work in — and a
  stand-down looks exactly like a broken feature. The bar is 140: the panel's own
  columns plus a work pane worth working in. Excluding VS Code was the other
  half of that number's job, and that belongs to the client check, which
  inspects the client instead of guessing from width.
- A panel restored by [tmux-resurrect](https://github.com/tmux-plugins/tmux-resurrect)
  came back as an empty shell, and mux — which finds the panel by what the pane
  was started with — did not recognise it and opened a second panel beside it, so
  every server restart left a three-way split. The plugin cannot restore
  `mux watch`: it saves a pane's *child* process and the panel has none, being the
  pane's process itself, so `@resurrect-processes` never matches. The panel now
  marks its pane with the tmux pane title — the one thing a restore hands back
  intact, since it saves no user options at all — and closes the dead pane before
  opening a live one in its place. The title is matched whole, and the cleanup
  runs before the width and VS Code checks, since a window that will not get a
  panel is the one that wants those columns back most.

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
