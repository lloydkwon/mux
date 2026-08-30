# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## What this is

`mux` is a TUI tmux session manager (Go + Bubble Tea) that shows a live preview of every session, detects AI CLIs (`claude`, `codex`, `aider`, `gemini`), and displays git branch / worktree status and Claude token cost.

This repo is a **personal fork**, third in a chain: `lunemis/mux` → `xguru/mux` → this repo. The module path is `github.com/lloydkwon/mux`. Fork-specific features: the "New shell" / "New tmux session" action rows at the top of the list, persistent per-session ordering (`0`–`9`, `o` to rotate sort), and the Claude progress state badges.

Remotes: `origin` is this fork (`lloydkwon/mux`, SSH), `upstream` is the fork it came from (`xguru/mux`). Push over SSH — the `gh` OAuth token lacks the `workflow` scope, so HTTPS pushes are rejected for touching `.github/workflows/`. Use `go install ./cmd/mux` to install the working tree; `@latest` installs the published branch instead.

## Commands

```bash
make build                       # → ./mux, injects version via -ldflags
make test                        # go test ./...
make install PREFIX=~/.local     # installs to $PREFIX/bin

go test ./ui -run TestFlattenMenu -v    # single test
go vet ./... && golangci-lint run       # expected before submitting (no repo config; defaults)
GOARCH=arm64 go build ./cmd/mux         # CI also cross-compiles this

scripts/test-fixture.sh up      # real tmux sessions (multi-window/pane/empty) for manual TUI checks
scripts/test-fixture.sh down    # tear them down
```

CI (`.github/workflows/ci.yml`) runs test + build + arm64 cross-build. Releases are tag-driven via goreleaser. Commits follow Conventional Commits.

## Architecture

Three layers, strictly one-directional:

- `cmd/mux` — cobra CLI: root (TUI), `popup`, `setup-keybind`, `setup-panel`, `status`, `watch`, `panel`, `nav`.
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

Every tick is *stateless*: `sessionsLoadedMsg` replaces `m.sessions` wholesale and the previous slice is dropped, and nothing in the TUI compares one tick against the last. The only cross-tick history in the codebase lives in `mux watch`, not here — see below.

### Flattened tree list model

There is no nested view. `ui/tree.go`'s `flattenMenu` produces a flat `[]listItem` — action rows, then each session, then (if expanded) its windows, then (if expanded) their panes — and `Model.cursor` is an index into that slice. `treeState` holds the expansion sets plus the window/pane caches; expansion survives session refreshes, and `pruneCaches` drops entries for sessions that vanished.

Call `rebuildItems()` after any change to sessions, filter text, ordering, or expansion state, or the cursor will point at a stale row. Action rows are only included in the unfiltered top-level view.

### Click selects, double click commits

Mouse reporting is on (`tea.WithMouseCellMotion()` in `cmd/mux/main.go`), which costs the terminal's own scroll and drag-select — Shift still gets them. A press maps to an item index through `chromeTop()` (title, plus the filter/error bar when there is one), the column's border, and `listOffset` — the *same* function `renderListView` scrolls by, so a row the user can see is a row they can hit. `chromeTop`, `panelHeight`, `listWidth` and `extraBar` exist for that reason: `viewMain` used to compute them inline, and a click map that recomputed them separately would drift the moment the chrome changed.

A single click only moves the cursor. Unlike the watch panel — where a click switches outright because there is nothing to read first — the right column here *is* the thing to read, so clicking to look and clicking to leave must not be the same gesture. The second press on the same row inside `doubleClickWindow` commits, and it commits through `activateCurrent()`, which `enter` also calls: a click that attached somewhere other than where the key would is a bug nobody would think to look for.

### Preview targeting

`previewKey{session, window, pane}` uses `-1` as "the active one"; `.target()` renders it as tmux target syntax (`sess`, `sess:1`, `sess:1.2`). The model caches one preview blob plus the key it belongs to, and `viewMain` only renders it when that key still equals `previewKeyForItem(currentItem)` — otherwise the pane shows blank rather than the wrong session's output.

### Attaching happens after the TUI exits

Never attach from inside the Bubble Tea loop. `Update` records `attachTarget` / `detachRequested` and returns `tea.Quit`; `cmd/mux/main.go` inspects the returned model and then calls `ui.AttachToSession`, which `select-window`/`select-pane` first (attach replaces the process) and then either `switch-client` (inside tmux, `$TMUX` set) or `syscall.Exec` of `attach-session` (outside). "New shell" only detaches when `$TMUX` is set; outside tmux, quitting already returns to the login shell.

### Modal sub-models

`mode` selects which `updateX` handles input. Sub-models (`create`, `rename`, `filter`, `confirmKill`, `order`) are plain structs with their own `Update`/`View`; they report completion by emitting a message (`sessionCreatedMsg`, `sessionRenamedMsg`, `sessionOrderMsg`, …) that the *top-level* `Update` handles before mode dispatch — that is where mode is reset to `modeList` and side effects like preference writes happen.

### The sidebar lives in `mux watch`

The panel is a pane on the **right** of the window (`split-window -h`, `tmux/panel.go`). It opened on the left until a phone made the case against it: a client narrower than the window shows the leading columns, so a panel on the left is the half you can see and the shell is the half you cannot. The pane the user types in gets the left edge. `notifyLines` (`ui/notify.go`) renders the whole thing borderless — the tmux pane already draws one — and `watchModel.View` drops it into `fixedBox`.

**The panel's session header is opt-in, and off by default.** `panelHeaderLines` (`ui/notify.go`) draws the selected session's name, branch, state and directory above the list; `@mux_panel_header` (global, `on`/`1`/`true`/`yes`) turns it on, and `PanelHeaderEnabled` (`tmux/panel.go`) is read **once** in `RunWatch`, the same way `MinWindowWidth` is — a preference does not change between ticks.

The default is off because `mux border` now puts the same facts on the top border of the pane you are in, and the panel opens with its cursor on your own session (`autoSelect`), so the header would be a second copy of the line right beside it. What it still answers is the case the border cannot reach: move the cursor with `M-Up`/`M-Down` and it describes a session you are *not* in. That is why it is an option rather than a deletion — the third thing in this panel's history to be removed for costing more chrome than it returned, and the first to be kept behind a switch.

**The panel draws no session's screen, deliberately.** A detail column that previewed the selected session was built and removed: it took half the panel to show a copy of a pane, and the copy you most wanted was usually the real pane sitting right beside it. What the panel shows is what a terminal cannot — which sessions exist, what state each is in, and what they *just* finished. The rest of the window stays your shell.

Because there is nothing to read first, **a click switches**. The keyboard keeps two steps only because `send-keys` cannot point and commit at once: `mux nav up/down` moves `m.selected` and `enter` commits it. That cursor is a session *name*, not a row index — rows are ordered by how long a state has held (`sortByDisplayedAge`), so an index means something different two seconds later. `reselect` re-anchors it every refresh.

**`ownSession`** is resolved once in `RunWatch` (`tmux.SessionForPane(selfPane())`). It marks that row `◀` and is where `autoSelect` opens the cursor, so the panel says where you are before it says anything else. An empty `ownSession` means "could not tell" and falls back to the top row.

`autoSelect` used to *skip* it, reasoning that `enter` there would go nowhere. Both halves were wrong, and the fix is worth not undoing. There is one panel per window, so switching sessions puts you in front of a panel that has never been told anything — skipping meant it opened with an unrelated session highlighted, which is a sidebar failing at the one thing a sidebar is for. And `enter` there is not a no-op: `switchToSession` calls `restoreFocus` before it switches, so on the session you are already in it hands you back to the pane you were working in.

**The cursor re-anchors to `ownSession` when a client *arrives*, and that is an edge, not a state** (`reanchorOnArrival`). `reselect` only re-picks when the selected name has vanished, so a cursor the user moved was otherwise kept forever — and the one reason to move it is to *leave*, which meant the panel you came back to was still pointing at the session you went to, `◀` and the highlight naming different rows with nothing to reconcile them. The signal is `Session.Attached` for the pane's own session, which `ListSessions` already carries every tick, so it costs no extra call. It has to be the `false → true` transition: "my session is attached" is true the whole time you sit in it, so acting on the level would drag the cursor back two seconds after every `M-Up`. What it cannot tell is *which* client arrived — with two clients on one server, a second one landing here re-anchors this cursor too.

Blank lines in the session column are layout, not decoration: a session's trailing blank carries that session's name, which is what makes a click target two rows tall. Group and section breaks carry none, so they do not stretch the block above them.

**`restoreFocus` is guarded by `PaneActive`, and the guard is load-bearing.** tmux's `MouseDown1Pane` runs `select-pane` before forwarding a click, so after a click the panel *is* active and `select-pane -l` is exactly the undo — without it, the window keeps reporting the panel's directory as its session's own (`tmux/panel.go`'s `-c` comment). Keys arrive by `send-keys` and never make it active, and restoring there selects whatever the window visited before the pane the user is in. Measured: `enter` from a three-pane window moved focus to the third pane.

### Typing `mux` outside tmux gets the popup too

`prefix + m` draws mux in a `display-popup`; typed in a terminal that is not in tmux it drew itself in that terminal instead — the same program in a different shape. `runRoot` (`cmd/mux`) closes that gap by attaching first and popping up on the client that creates (`AttachAndPopup`, `tmux/popup.go`).

Attaching has to come first, and that is not a preference. Measured: with a server up and a client attached elsewhere, `display-popup` succeeds with `$TMUX` unset — and draws on *that* client, in a different terminal from the one you typed in. A popup needs a client, so outside tmux there is nothing to draw on until this terminal becomes one. `attach-session` with no `-t` takes tmux's own most-recently-used session; which session to attach to is the question mux exists to answer, but asking it needs a client, so tmux's default gets us on screen and the popup that follows is where the answer is given.

**`MUX_NO_BOOTSTRAP` is the recursion guard and it is load-bearing.** The popup runs mux, so without it the child bootstraps in turn and the popups never stop. `$TMUX` *is* set inside `display-popup -E` — measured, it reads back as the socket and a pane id — so that check would work today, but the cost of being wrong is an unbounded loop and an env var mux sets itself depends on nothing. It rides in the popup's command as a `VAR=1 exec …` prefix, which works because tmux runs a shell-command through a shell.

**Quitting that popup detaches, and that is the other half of the feature.** The terminal was attached only so there would be a client to draw the popup on, so `q` — no session chosen — has to give it back; otherwise closing the popup drops the user into whichever session `attach-session` picked, which is neither where they typed `mux` nor a session they chose, and getting out means knowing tmux's detach key. `runTUI` (`cmd/mux`) reads `BootstrapPopupEnv` and routes that case through `ui.DetachClient`, the same call `New shell` already makes from inside a popup.

`MUX_BOOTSTRAP_POPUP` is a **second** variable rather than `MUX_NO_BOOTSTRAP` doing double duty, because they answer different questions. The guard says "do not bootstrap", which a user may reasonably export in their own shell to opt out; the mark says "you are the popup a bootstrap opened", and only mux ever sets it. Read as one, an opted-out `mux` in a plain terminal would try to detach a client it never attached.

It stands down with no sessions: there is nothing to attach to, and creating one to attach to means naming it for the user, while the list mux draws instead already offers `New tmux session`, which asks. Every other failure — old tmux, no server, no executable — falls through to drawing in place, which is what mux did before.

### The line above the pane you work in

`pane-border-status top` plus a `pane-border-format` that calls `mux border` (`ui/border.go`, `cmd/mux`'s `runBorder`, installed by `SetupPanel`) puts the session's summary — name, directory, tool, live state, branch — on the *other* column, above the shell. It is the only row of that side mux gets to write in: the pane is the user's terminal, and its title already belongs to whatever runs there (Claude Code names its pane after the task in hand, so taking it over would cost more than the line gives).

`BorderLine` breaks three of this codebase's own rules on purpose. It emits **no ANSI**, because tmux styles formats with `#[fg=…]` and paints raw escapes as text — colour is left to `pane-border-style`, and the state glyph carries what a colour would have said. It **does not right-align**, because tmux fills what the format leaves unused with `─`, so padding to the full width would replace that run with blanks and break the border. And it **gives up its parts from the right** — branch, then the tool's name, then the directory — never the session name, since a border that cannot say which session it belongs to has nothing worth drawing.

Two things were measured rather than assumed. `#()` **does** re-run inside `pane-border-format` (verified on an isolated server: renaming a session changed the line without a restart), on the `status-interval` cadence — the manual only documents this for the status line. And the border **costs one row per pane**, the panel's included: measured 20 → 19 for one pane, 20 → 18 with a second work pane.

The format's conditional skips the panel's own pane by matching `panelCommand` against `pane_start_command`. Deciding it in tmux rather than in `mux border` means the panel spawns no process at all, and `mux border` never has to work out which pane it is being asked about. **The line carries two levels of quoting for two parsers**: double quotes for tmux (which is what stops `#` in `#{pane_id}` starting a comment) and single quotes around the path inside `#()`, which `/bin/sh` reads — `TestPanelBlockQuotingKeepsPaneIDInsideQuotes` pins both.

`mux border` is a fresh process per pane per refresh, which is why it uses `tmux.SessionForTarget` rather than `ListSessions`: one `display-message` with list-sessions' own format, handed to the same `parseLine`. It prints an empty line and exits 0 on any failure — an error string here is not reported anywhere a user can act on it, it is simply painted across the top of the pane they are working in and stays there.

**The panel has two keyboards, and that is deliberate.** `prefix + Tab` (`FocusPanel`, `tmux/panel.go`) steps *into* the pane and back out; while you are in it, `watchModel.handleKey`'s own `↑↓ j k g G enter` apply directly, and `esc` leaves without choosing. Stepping back out is `select-pane -l`, the same mechanism `restoreFocus` uses after a click, so it returns to the pane you came from however many sit beside the panel. A window with no panel does nothing and exits 0 — the binding is global, and opening one is `prefix + a`'s job.

The second keyboard is the one that does *not* move the focus, and it remains the default way to use the panel:

**Keys reach the panel without focus reaching it.** `mux nav <up|down|top|bottom|enter>` resolves the window's panel pane and `send-keys` to it. The directions are a vocabulary, not raw key names, so `handleKey` can change without every user's tmux.conf changing. A window with no panel exits 0 — the binding is global and a failing `run-shell` writes to the status line on every press. `mux setup-panel` installs these as `M-Up`/`M-Down`/`M-Enter` (`navBinds`): no prefix, so steering costs one keystroke, and Alt rather than the arrows alone so they cannot collide with tmux's own `prefix + ↑↓` pane navigation.

`q` still *quits* `mux watch`, closing the pane. Now that the panel can hold the focus that is a key someone may press by habit, so `esc` is the one documented as leaving; a panel closed that way comes back on the next hook or on `prefix + a`.

The panel speaks Korean and the TUI speaks English. `aiStateLabel` (`ui/notify.go`) is the panel's side of that and `AIState.String()` is the TUI's. Do not mix them in one pane. `aiWaitingLabel` is the same rule for *what* a blocked session is waiting on — but only for Claude's two fixed phrases (`input needed`, `permission prompt`); the field otherwise carries the command Claude wants to run (`Bash: git push`), and a command is not prose to translate, so anything unrecognised passes through verbatim. `ui/status.go` leaves it raw on purpose: that surface already prints `AIState.String()`, so it is the English one.

**The detector remembers a timestamp, not just a state** (`aiSnapshot`). Claude stamps every status change, one blocked prompt replacing another included, and with only the state to compare those look exactly like no change — the panel went silent on the transition it exists to report. `newPrompt` is the extra rule, and it needs *both* sides stamped: everything screen detection finds arrives with `AISince` zero, and treating that as "changed" would make every tick of a blocked session an event.

**A session that goes away gets one line** (`○ 종료`), then is forgotten. It is the one transition with no state left to report — the badge that would have said so went with it, and without the line the row just stops being drawn, which reads the same as a session going quiet. Only sessions that held a live state qualify; closing a plain shell is not news. The glyph is `○` because `TestGlyphWidthsAreStable` already pins it at one cell, and a state glyph that measures wrong shifts every column after it.

Two action rows — `셸로 나가기` and `새 세션` — were added above `🔔 AI 세션` and then removed at the user's request. Worth knowing if they come up again: the first attempt drove them through tmux's `command-prompt`, which failed in the field with a status-line error mux could not report (everything after the prompt appeared happened outside this process). The second ran `mux new` in a popup, which worked. The rows themselves went because the panel is read at a glance and two rows of chrome at the top cost more than they returned.

`mux watch` is a pane and not a popup for a reason worth not re-litigating: tmux has no floating window that leaves the keyboard alone. `display-popup` is documented as "Panes are not updated while a popup is present" — it freezes what is behind it and takes input. A pane is the only always-visible tmux surface that still lets you type, which is also why the companion `my-mux` widget is a GTK3 dock window on the desktop rather than anything inside a terminal.

It runs as a **separate process**, so it shares no state with the TUI: its own `prevAIStates` — the codebase's only cross-tick history, since a transition can only be seen by diffing — its own TTL caches, and a slower 2s tick (the 500ms rate exists for the TUI cursor row's preview, which reacts to a keystroke; this one reacts to a click).

**The event log is the one thing panels do share**, and it lives in the tmux server: the global option `@mux_events` (`tmux/eventlog.go`), read and rewritten by every panel on each tick. Detection stays per-process — only a diff can see a transition — but what it observes goes somewhere all of them read. Without that, a panel created a minute ago opens on `아직 없음` beside panels showing hours of history, because `detectTransitions` records a first sighting rather than reporting it, and every session is a first sighting to a new process.

There is no lock and none is needed. Every panel watches the same server-wide session list, so one transition is observed by all of them and written by each; the redundancy *is* the retry, and a write that loses the race drops an entry another panel is about to write again. What that demands instead is that the same transition produce the same entry everywhere, which is `duplicateAt`'s job: `(session, state, AISince)` when Claude timestamped the state itself, and "same session, same state, within ten seconds" for everything screen detection finds — `ScreenState` carries no timestamp, so every tool but Claude arrives with `AISince` zero.

Two things about the panel side are easy to undo by accident. The merge is a **`tea.Cmd`**, not work done inside `Update`: `ui` has no mock runner and several tests call `Update(sessionsLoadedMsg{…})` directly, so merging inline would make `go test ./ui` write options into the developer's own tmux server. And `switchFailedMsg`'s `⚠ 전환 실패` stays in `watchModel.local` — it says *this pane's* click failed, which is not news in anyone else's window. `combineEvents` interleaves the two by time at render.

The log dies with the tmux server, which is right for something called "recent events" — and is why it is an option rather than a file beside `panel.json`.

Every tmux call it makes must name its own pane (`selfPane()`, from `$TMUX_PANE`). tmux resolves an omitted target to the window's *active* pane, which is the one the user works in — the panel is created detached and never becomes active on its own. A width correction sent without a target shrank the wrong pane, and the panel grew to fill what it gave up.

**The panel names its own pane, and that title is the only thing that survives tmux-resurrect.** `MarkPanelPane` (`tmux/panel.go`) sets `panelTitle` from `RunWatch`, and `findGhostPane` closes a pane that still carries it but is no longer running the panel.

That indirection is forced. tmux-resurrect cannot restore `mux watch` itself: its save strategy records a pane's *child* process, and the panel is the pane process with no child, so the command it saves is empty and `@resurrect-processes` never matches — verified against the plugin, not assumed. What comes back is a shell in the panel's place, at the panel's width, whose `pane_start_command` now reads `cat <contents>; exec $SHELL`. Restore saves no user options at all, so `@mux_panel_*` cannot carry the mark either; it does restore the title verbatim, with no option to turn that off. Without the mark, `findPanelPane` sees no panel and the hooks open a second one beside the dead one — a three-way split on every server restart.

Three things about the cleanup are deliberate. It is a **second** `list-panes` rather than another field on the first, because the hook path almost always finds a live panel and must stay at one call — `TestTogglePanelDoesNotLookForAGhostWhenThePanelIsThere` pins that, and changing `findPanelPane`'s format string would also break twenty mock keys. The title is compared **whole**, since this closes a pane and the only thing worse than a leftover one is closing the pane someone was in. And it runs **before** the width, VS Code and manual-off checks: a window mux refuses to put a panel in is the window that wants those columns back most.

**A ghost that is the window's only pane is untitled, not killed.** An exact title is not proof either: resurrect restores titles verbatim, so a user who kept working in the restored shell owns a pane named exactly `mux panel` — and when the window holds nothing else, `kill-pane` is `kill-session`. Measured 2026-08-30: two sessions lost that way, via nothing more exotic than closing the panel and reopening it. `findGhostPane` counts the panes off the same `list-panes` output (no extra call), and the alone case clears the title with `select-pane -T ""` — the pane is never mistaken for a ghost again, and the split that follows puts the real panel beside it. With another pane in the window the ghost is still killed; that is the case the cleanup exists for, and there it does not take the session with it.

Marking happens in `RunWatch` rather than at split time so a panel someone started by hand with `mux watch` is marked too — the same reason `panelCommand` matches that pane. A panel created before this existed has no title and is not recognised; it heals the first time one is opened and saved.

**The width the panel opens at has three sources, most specific first**: the session's `@mux_panel_width`, then `SavedPanelWidth()` from `~/.config/mux/panel.json`, then `defaultPanelWidth` (36). `rememberPanelWidth` writes the first two on every drag, because they answer different questions — the tmux option is per session and lets two sessions on one server differ, but it dies with the server, so the disk copy is what makes a dragged width survive a restart. The disk copy is deliberately one number and not a per-session map: session names are exactly what a server restart does not carry over. It is deliberately not `preferences.json` either — the TUI reads that into memory at startup and writes the whole struct back, so a width saved by the separate `mux watch` process would be clobbered by the next sort toggle. `MinPanelWidth` (`tmux/panelwidth.go`) is the floor on both ends of that round trip, which is why `ui`'s `notifyMinWidth` is an alias of it rather than its own 24: a hand-edited `panel.json` must not open a pane the renderer immediately gives up on. `defaultPanelWidth` is the narrowest the panel still answers its questions at — below about 32 the branch column goes and event text gets cut — not the narrowest it survives, which is much lower because a user dragging deliberately gets to choose.

**A pane appearing or dying beside the panel is not a drag, and the width alone cannot tell.** `applyResize` reads `#{window_width}` and `#{window_panes}` in one call (`tmux.WindowShape`) — one call because a width read now against a count read a tick ago would misread a split as a drag, which is the thing the count is there to prevent. A changed count means tmux re-laid the window out; only an unchanged one can be a border drag.

The ceiling below was supposed to cover this and could not. tmux hands a closing pane's columns to a neighbour, and in a two-pane window that lands on *exactly* half — which is not **over** half, so it passed, was saved to `panel.json` and then actively held. Observed in the field at 118 of 237 columns. Tightening the comparison to `>=` is not the fix: the correction target is itself `maxPanelWidth`, so the panel would restore to a width it immediately rejects, forever.

Pane width is held against tmux, not just recorded. With `aggressive-resize`, switching sessions resizes windows and tmux redistributes panes, so the panel drifted every switch. `applyResize` separates the two causes by reading the *window* width: unchanged window plus changed pane is a border drag (adopt it), changed window is a re-layout (undo it). Three rules keep it from running away. The window width is read **synchronously**, because getting it via a `tea.Cmd` let a stale value read as "window unchanged" and a momentarily squeezed pane became the enforced width. A width below `notifyMinWidth` is never adopted as intent. And a width above `maxPanelWidth` — half the window — is not either, because "same window, different pane" is *also* what a pane appearing or dying beside the panel looks like; measured, the panel reached 188 of 237 columns and held there, leaving 48 to work in. The ceiling both puts the pane back and caps the first size seen, so a width remembered from a roomier window heals instead of persisting.

`modeHelp` is the deliberate exception: it carries no state, so it has no sub-model struct, no `Model` field, and no completion message — just an enum value, a `case` in the mode dispatch that resets to `modeList` on any `tea.KeyMsg`, and `viewHelp` in `View`. Don't add a `helpModel` to make it match the others. It also renders through `fixedBox` instead of `viewWithOverlay`, because a centered box has no size bound and the page is taller than a `mux popup` gets on an 80x24 terminal (68x19) — that budget is pinned by `TestHelpBodyFitsPopup`, and adding a line to `renderHelpBody` will fail it.

### The palette is the terminal's

`ui/styles.go` names roles — accent, muted, danger — and fills them with **ANSI palette indices**, never hex. The user's scheme supplies the values, so a light scheme gets colours its author picked for a light background and a dark one gets colours picked for a dark background. Ordinary row text sets no colour at all, taking the terminal's own foreground.

It was hex once, all of it chosen against a dark terminal. Measured on GitHub Light (`#F6F8FA`, this fork's author's actual scheme), the colour every list row was drawn in reached **2.4:1** and the accent **1.7:1**, against a 4.5:1 floor. The screen was not ugly so much as barely there. `tmux/aitools.go`'s tool colours are indices for the same reason.

`lipgloss.AdaptiveColor` was the obvious alternative and is deliberately not used: it resolves by asking the terminal for its background over OSC 11, and `mux watch` runs inside a tmux pane where the answer may never arrive — which would draw the panel in the opposite palette from the TUI beside it. An index has nothing to ask.

**A selected row is reverse video, and `renderRow` drops segment colours on it.** That second half is not optional: under reverse the terminal swaps foreground and background, so a colour set on a span is painted as its *background* and the row grows green and red blocks. The row inverts as one piece and the state glyphs (`⏳❗✅`) still carry what the colour would have said. This is the tempting thing to undo — `TestSelectedRowDropsSegmentColors` is there to catch it.

### One frame, not two boxes

`viewMain` draws a single `drawFrame` around both columns with a `┬ │ ┴` divider, and `renderListView` / `renderPreview` return **unframed** content of exactly the inner size. Two separate boxes put two border characters side by side down the middle of the screen, which reads as a seam. Consequences worth knowing: the two inner widths plus three frame characters are the whole terminal (`m.listInnerWidth()` + right + 3 = `m.width`), and the mouse map has to exclude both the outer frame and the divider — `x == 0` and `x == listInnerWidth+1` are not rows.

### Manual fixed-size rendering

`ui/layout.go` (`padOrTruncate`, `fixedBox`, `drawFrame`, `renderRow`) does the layout by hand instead of using lipgloss containers, because `capture-pane -e` output carries raw ANSI that must be clipped to an exact cell width. Two consequences:

- Every rendered panel must return exactly `panelHeight` lines and exact widths, or the two columns desynchronize.
- **Width compensation is impossible here.** The frame re-pads every line to the column width, so any cell a row subtracts to account for a wide glyph is added straight back. The only workable rule is to emit glyphs whose drawn width equals `ansi.StringWidth` — verify a new glyph before using it (`TestGlyphWidthsAreStable` in `ui/list_test.go` pins the current set). Emoji measure and draw 2; most geometric shapes measure and draw 1.
- **Never wrap a row containing nested styled spans in an outer style.** A nested `lipgloss.Render` emits its own `ESC[0m`, which resets the *background* too, so a colored segment mid-row strips the selection highlight from everything after it. `renderRow` (`ui/layout.go`) builds rows from `rowSeg` values where each span re-states the full style, background included.

### One AI badge, two facts

AI CLI detection is a single feature, not a generic layer with a Claude feature bolted beside it. `tmux/aitools.go` owns all of it: `aiToolMap` (which tools exist, their icon and color) *and* `AIState` (`None`/`Working`/`Approval`/`Ready`) with `AIState.Icon()` (`⏳`/`❗`/`✅`).

**The rule: a live state glyph replaces the tool icon, it never sits beside it.** `ui/status.go`'s `aiGlyph` is the one place that decides, and `ui/list.go`, `ui/preview.go`, and `mux status` all go through it. A separate state column would repeat what the tool icon already says, since only a detected AI CLI can have a state. Keep it that way when adding anything state-related.

The badge cell is padded to `badgeWidth` (2) even when empty, because state glyphs measure 2 and tool icons measure 1 — that padding is what keeps the git branch in one column across every row.

**The list is a table, and its columns are decided once per render.** `renderListView` computes `sessionNameWidth` and `anyOrdered` over the *whole* list rather than the visible slice, then hands both to every row: a column sized per row, or per screenful, would move the names as you scrolled. The name leads because it is what the eye looks for; the age, badge and branch follow it in fixed columns so the list is scanned down rather than read across, and the branch is flush right and is the first thing to yield when the row runs out of room.

Two markers became colours instead of columns, which is what freed the gutter from twelve cells to two. The attached session is its name in `colorAccent` (`nameColor`), the active window or pane likewise (`activeColor`) — a marker column costs every row a cell to say something about one of them. The `#3` order label is drawn only when some session in view carries an order.

The list's elapsed column shows the *state's* age for a session with live state (`sessionAge`) and the session's creation age otherwise, and takes the state color — with the glyph folded into the badge, that color is what still marks a blocked row at a glance.

### Claude live-state chain

Claude is the only tool that publishes live state, so `tmux/claude_status.go` is the sole producer of a non-zero `AIState`. It reads `~/.claude/sessions/*.json` — Claude Code's own state file, one per running session — and indexes them by tmux session name via each file's `tmux` field (`"<session>:@<win>.%<pane>"`). `ClaudeStatuses()` does one `os.ReadDir` per refresh behind a 1s TTL cache, and `ListSessions` hoists that single call out of its parse loop, so state arrives atomically with the session slice and needs no tick fan-out or `Model` cache.

Four things about this data are easy to get wrong:

- **The file is written only on status change, so mtime is not a heartbeat.** A crashed session leaves its file behind and would display as permanently busy. Liveness is `processAlive` (`tmux/proc.go`): signal-0 for existence, plus a best-effort `/proc/<pid>/stat` field-22 comparison against the file's `procStart` to catch PID reuse. The `/proc` half no-ops on macOS by design — no build tags.
- **`status: "waiting"` means blocked on the user**, whatever `waitingFor` says; a finished turn sitting at the prompt reports `idle` instead. That mapping lives in `mapClaudeState`.
- **`status: "shell"` outranks both `idle` and `busy`, and it is sticky.** Claude reports it for as long as *any* background shell is alive, so a session that left a dev server running never reaches `idle` — its turns end invisibly and anything watching for a turn to finish waits forever. Measured: a session actively generating output reported `shell` for 13 minutes because one server was up. `demoteServerShell` (`tmux/claude_status.go`) recovers the common case by reading the Claude-owned children under `/proc/<pid>/task/<pid>/children` (identified by `shell-snapshots/` in their cmdline, which excludes MCP servers) and promoting to `AIStateReady` when *every* one of them matches `serverCmdPattern`. Anything else — a build, a test run, a download — is real work and the shell state stands, as does "no jobs found", since no `/proc` must not read as "the turn ended". Ported from my-mux's `demoteServerShells`; keep the two pattern lists in step.
- The format is private and unversioned. Every field decodes as optional; a renamed field must degrade to "no badge", never an error.

Because `Session.ActiveCommand` only reflects the *active pane of the active window*, this state file also covers Claude running in a background window. `tmux.SessionAITool` prefers it over `ActiveCommand` so the badge, token loading, and `mux status` all catch that case — and it hardcodes `"claude"` for the stateful path, since a second state provider would first have to carry its own tool name onto `Session`.

### Claude cost tracking chain

`tmux/claude.go`: pane PID → `pgrep -P` children → `~/.claude/sessions/<childPID>.json` → `{sessionId, cwd}` → `~/.claude/projects/<cwd with "/" replaced by "-">/<sessionId>.jsonl` → sum `usage` fields on `type: "assistant"` lines. Per-1M pricing constants are hardcoded in `estimateCost` (currently Opus-tier rates) and are an estimate only. `ui/app.go` gates the per-tick load on `tool.Name == "claude"` — any other AI CLI would scan for a file that is not there.

### Preferences

`ui/preferences.go` stores sort mode and the `{session name: order}` map in `os.UserConfigDir()/mux/preferences.json`, written atomically (temp file + `chmod 0600` + rename). `normalized()` is the validation gate — it runs on both load and save and drops blank names / non-positive orders. Rename and kill both fix up the `Orders` map so entries don't leak.

### tmux config editing

Two owned regions, deliberately separate.

`SetupKeybind` (`tmux/popup.go`) writes an idempotent, marker-tagged (`# mux popup keybinding`) bind line. It detects gpakosz/.tmux ("oh-my-tmux") by symlink target or the `# : << 'EOF'` first-line signature, and for those installs writes to `.tmux.conf.local` *before* the `# "$@"` sentinel — writing into the main `.tmux.conf` corrupts oh-my-tmux's heredoc. It also strips legacy untagged installer binds. Preserve all of that when touching this file.

`SetupPanel` (`tmux/setuppanel.go`) writes the panel binding plus the hooks that keep a panel in every window, as a fenced `# mux panel { … }` block. **The fence must not become the popup's per-line marker.** Two reasons: `stripMarkerLines` runs on `SetupKeybind`'s oh-my-tmux path and would delete the hooks as collateral, and two tests assert the popup marker appears exactly once. The single-line helpers cannot express a multi-line region anyway — `upsertBindLine` replaces every tagged line, `writeBindToLocal` only the first.

`upsertBlock` places a first-time block above oh-my-tmux's sentinel, else above a trailing tpm loader (tpm documents it as having to be last, and the comment sitting on top of it comes along), else at the end. An opening fence with no closing one is treated as absent — truncating a hand-edited config is not worth the convenience. Writes go through `os.WriteFile`, not temp-file-and-rename, because it follows symlinks and oh-my-tmux installs `~/.tmux.conf` as one.

**Every generated line is read by two parsers, and only the outer one is tmux's.** The run-shell argument is double-quoted for tmux — that is what stops `#` in `#{pane_id}` starting a comment and what defers its expansion — and the path inside it is single-quoted for `/bin/sh`, which is what actually runs it. Measured: `run-shell "/…/my tools/mux panel --auto -t %2"` does not run at all, and the only symptom is a line on the status bar. `borderLines` had this from the start; the binds, the nav binds and the hooks did not, and a path with a space silently disabled all of them.

The hooks are a **`{ }` block** rather than a single-quoted string, and that is forced rather than stylistic. tmux has no escape inside single quotes, so a value carrying the shell's own quotes cannot be written that way — measured, it is a parse error (`too many arguments`), not a silent mis-parse. Braces defer the whole value, which is what the hook needs anyway: `#{pane_id}` must be the pane the hook fired for, not the pane that happened to be active when the config loaded.

**Mux repairs its own regions when they name an older copy of itself** (`tmux/repair.go`, called from `runTUI`). Both setup commands write an absolute path resolved once from `os.Executable()`, and that path is right until mux is installed somewhere else — a different `PREFIX`, a `go install` after a release tarball, a `GOBIN` that moved. Nothing said so: tmux went on calling a binary the user believed they had replaced, and deleting the old copy was worse rather than better, since every hook then failed on every window event with an error that only reaches the status line.

The comparison is against the *whole* regenerated block, not just the path, so a region written by an older mux heals its shape too — that is how existing installs pick up the quoting above. The keys are read back out of the block rather than reset, and a region whose keys cannot be read is left alone: a hand-edited block is not ours to guess at. `applyToServer` then sources a temp file holding only those lines, so the fix takes effect now instead of at the next reload, without re-running the user's whole config as a side effect of opening the TUI.

**One width decides both whether a panel opens and whether an open one stays.** `tmux.MinWindowWidth()` is that number — `DefaultMinWindowWidth` (140) unless `@mux_panel_min_width` overrides it globally. The create gate reads it in `TogglePanel`'s `--auto` branch, and `watchModel.applyResizeWith` quits below it; a window mux would refuse to open a panel in is a window an open one should not sit in. `mux watch` resolves it once at startup (next to `ownSession`) so `applyResizeWith` stays pure and a resize costs no extra tmux call.

**Leaving takes two narrow readings in a row, and a width of zero is not a reading.** Switching sessions re-lays-out every window tmux touches, and a window can report a width it holds for an instant; quitting on one closed panels during an ordinary session switch. What that cost the user was not just the sidebar — with the pane gone, `mux nav` had nowhere to send keys and exits 0 by design, so the panel simply stopped answering with nothing on screen to say why. The asymmetry is the argument: staying one tick too long in a narrow window is a cramped sidebar, while leaving wrongly is unrecoverable until someone finds `prefix + a`.

**The hook path never reports a failure.** `mux panel --auto` is what the seven `panelHooks` run, `client-resized` among them, and a failing `run-shell` paints the status line of *every* attached client each time it fires. tmux captures `#{pane_id}` when a hook fires and runs the command a moment later, so the pane on the command line may already be gone — measured, `display-message -p -t <dead pane>` exits 0 and prints a space, which `panelWindow` correctly refuses, and that refusal became `'mux panel --auto -t %53' returned 1` on a permanent loop. `runPanelAuto` (`cmd/mux`) swallows it, in `cmd/` rather than in `tmux/` for the reason `runBorder` does: the package keeps reporting honestly, and "do not paint this on the status line" is the caller's policy. Pressing `prefix + a` still reports — that is an answer to a key the user just pressed, and it has somewhere to go.

The default was 200, picked so the width alone would also exclude VS Code's integrated terminal at 149-150 columns. That was too blunt: an ordinary Windows Terminal at **188** was silently refused a panel, and the `--auto` path stands down without an error, so it looked exactly like a broken feature. 140 is the panel's 48 columns plus a work pane worth working in, and VS Code stays `SessionOnlyInVSCode`'s job — which inspects the client rather than inferring from columns. The width rule now only stands alone for a window nobody is attached to, and there it may open a panel in a 149-column window it used to skip.

`panelHooks` is the list of events after which a window can be on screen without a panel. Each was checked against a real server, since a hook that silently never fires looks exactly like a broken feature. `client-session-changed` is the load-bearing one: clicking a session in the panel runs `switch-client`, landing you in a window that never had a panel. There is no recursion — the ensure runs `split-window`, which makes a pane rather than a window.

`install.sh` sets up the popup keybinding only; `mux setup-panel` is the sole path for the panel block.
