# mux

**Switch between AI CLI sessions without breaking your flow.**

> [!IMPORTANT]
> This is a personal fork, third in a chain:
> [lunemis/mux](https://github.com/lunemis/mux) →
> [xguru/mux](https://github.com/xguru/mux) → this repository.
>
> lunemis/mux and its contributors built the live preview, AI CLI detection,
> Git/worktree display, popup mode, and the core session manager. xguru/mux
> added the SSH-first start menu and persistent session ordering. This fork
> adds Claude Code progress state to the session list and preview.

Running Claude in one session, Codex in another, and a dev server in a third? Switching between them means detaching, listing sessions, remembering which is which, and reattaching. mux eliminates that friction — see every session's live output at a glance, spot which AI tools are active, and switch in a keystroke.

[한국어](README.ko.md)

![Go](https://img.shields.io/badge/Go-1.21+-00ADD8?style=flat&logo=go)
![License](https://img.shields.io/badge/License-MIT-blue.svg)

![Demo](assets/demo.gif)

## Personal fork additions

### Inherited from xguru/mux

- **New shell** — continue into the normal login shell when mux opens on SSH
  login. From inside tmux or a mux popup, detach the current client and return
  to the outer shell without killing the session.
- **New tmux session** — create a named session, optionally choose its starting
  directory, and attach immediately.
- **Persistent Order** — select a session, type a number, and press `Enter` to
  assign its preferred position (`0` clears it).
- **Sort rotation** — press `o` to rotate through recent activity, alphabetical,
  and explicit Order sorting.

### Added in this fork

- **The AI badge shows live state** — the badge that already told you *which*
  AI CLI a session runs now also tells you *what it is doing*: working (`⏳`),
  blocked waiting on you (`❗`), or done and ready for input (`✅`), with how
  long it has been that way. Tools that publish no state keep their own icon
  (`✦ ◈ ⬡ ✧`). The preview adds why it is blocked (permission prompt, input
  needed, and so on), and `mux status` puts the same badge in your tmux status
  bar.
- **An always-on sidebar** — `mux watch` keeps a narrow pane on the left of the
  window listing every session and what they just finished, while the rest of
  the window stays your terminal. Click a session to switch to it, or drive the
  cursor with `mux nav` from a tmux binding, which moves it without taking the
  keyboard away from the pane you are typing in.

If you want the original release and Homebrew package without these personal
changes, use [lunemis/mux](https://github.com/lunemis/mux).

## The Problem

In the age of AI-powered development, a typical workflow looks like:

- **Session 1**: Claude Code working on your feature
- **Session 2**: Codex reviewing your test suite
- **Session 3**: Dev server running your app
- **Session 4**: Another Claude session refactoring a different module

tmux's built-in `choose-session` shows you a list of names — but which session has Claude waiting for your input? Which one is still running? You end up cycling through sessions blindly.

## How mux solves it

### Live preview — every window and pane
See the actual terminal output of any session *before* you switch. Press `Tab` to expand a session into its windows, expand again to peek into individual panes — preview each one without attaching.

### AI CLI detection
`claude`, `codex`, `aider`, `gemini` are automatically detected and highlighted with badges — instantly find the right session. Where the tool publishes its own state, that same badge shows what it is doing (see below).

### Colours come from your terminal
mux names roles and lets your scheme fill them in — it draws with ANSI palette colours rather than hex, so it reads correctly on a light scheme and a dark one without a setting to change. The selected row is reverse video, which is legible in any scheme.

### Git branch & worktree display
Each session shows its current git branch. Linked worktrees are visually distinguished so you can tell at a glance which sessions are working on isolated branches.

### Cost & token tracking
For Claude Code sessions, mux reads session logs to display real-time token usage and estimated cost — no configuration needed.

### Live state in the AI badge
When a tool publishes its own state — today Claude Code — its badge stops being a static `✦` and starts showing what the session is doing right now, so you can tell at a glance which pane is waiting on *you*:

| | state | meaning |
|---|---|---|
| `⏳` | working | the tool is processing |
| `❗` | approval | blocked on a permission prompt or question — needs you |
| `✅` | waiting | turn finished, ready for input |

It is one badge, not two: the state glyph takes the tool icon's place rather than sitting beside it. Tools that publish nothing keep their own icon (`✦ ◈ ⬡ ✧`). The elapsed column next to the session name switches to how long the state has held, the preview panel adds the reason it is blocked (`❗ approval · permission prompt  3m`), and `mux status` puts the same badge in your tmux status bar.

This reads the tool's own state file rather than guessing from screen output, so it stays accurate even while the pane is scrolling.

### Popup overlay
Press one key to summon mux on top of whatever you're doing — even mid-conversation with an AI CLI. Pick a session and you're there.

![Popup mode](assets/popup.gif)

### Vim-style navigation
`j`/`k` to browse, `/` to filter, `Enter` to attach. No mouse needed.

## Quick Start

```bash
go install github.com/lloydkwon/mux/cmd/mux@latest
mux
```

For the best experience, set up popup mode (opens mux as a floating overlay):

```bash
mux setup-keybind               # binds prefix + m
tmux source-file ~/.tmux.conf   # reload config
```

Now press `Ctrl+b` then `m` anywhere in tmux to open mux.

## Installation

### Interactive installer

Builds with Go when it is available, and downloads a release binary otherwise.
It also offers to set up the popup keybinding.

```bash
curl -sSL https://raw.githubusercontent.com/lloydkwon/mux/main/install.sh | bash
```

### Go install

```bash
go install github.com/lloydkwon/mux/cmd/mux@latest
```

To build the checked-out working tree instead of the published branch, run
`go install ./cmd/mux` from the repository root.

### From source

```bash
git clone https://github.com/lloydkwon/mux.git
cd mux
make PREFIX="$HOME/.local" install
```

Installs to `$HOME/.local/bin/mux`. Use `sudo make install` for the default
`/usr/local` prefix instead.

### Upstream Homebrew package

```bash
brew install lunemis/tap/mux
```

> This installs the original upstream version, without the fork's features.

## Usage

### Basic

Run `mux` to open the session manager. Use `j`/`k` to navigate, `Enter` to attach, `q` to quit.

![Screenshot](assets/screenshot.png)

The left panel shows your sessions with AI badges and git branches. The right panel shows a **live preview** of the selected session's terminal output, updated every 500ms.

The first two rows are login-friendly actions: **New shell** closes mux and continues in the current shell; when selected from inside tmux or a mux popup, it detaches the current client and returns to the outer login shell without killing the session. **New tmux session** creates a named session and attaches immediately.

Press a digit while a session row is selected to set its persistent order. Continue typing for multi-digit values and press `Enter`; enter `0` to clear the order. Press `o` to rotate sorting between recent activity, session name, and explicit order. Preferences are stored in `~/.config/mux/preferences.json` (or the platform's user config directory).

### Popup mode (recommended)

Open mux as a floating overlay inside tmux — works even while AI CLIs are running in the foreground.

```bash
# Set up the keybinding (one-time)
mux setup-keybind          # prefix + m (default)
mux setup-keybind Space    # or use a different key

# Reload tmux config
tmux source-file ~/.tmux.conf
```

You can also open the popup manually with `mux popup`.

> **Note:** Popup mode requires tmux 3.2+

### Statusbar widget

Show AI session icons in your tmux status bar without opening the TUI:

```bash
# Add to ~/.tmux.conf
set -g status-right '#(mux status)'
```

This runs `mux status` which outputs a compact summary like `✦ ◈` when AI sessions are active.

### Always-on panel — `mux watch`

The list shows what each session is doing *now*, but not what it just finished. `mux watch` fills a narrow pane on the left of the window with your sessions and a log of recent state changes, so it is on screen while you work rather than only while the TUI is open. The rest of the window stays your terminal:

```
┌ mux watch 48 ──┬ your shell ─────────────────┐
│ 🔔 AI 세션     │ ❯ npm test                  │
│                │ ✓ 42 passed                 │
│ ❗ api 12s     │                             │
│    permission  │ ❯ _                         │
│                │                             │
│ ⏳ mux 3m ◀    │                             │
│                │                             │
│ ✅ web 2h      │                             │
│                │                             │
│ ── 세션        │                             │
│                │                             │
│    notes 3h    │                             │
│                │                             │
│ ── 최근 이벤트 │                             │
│ 13:00 api ❗   │                             │
│ 12:59 web ✅   │                             │
└────────────────┴─────────────────────────────┘
```

Sessions running an AI CLI come first; the rest sit under `── 세션` so every session is reachable without opening the TUI. Recent transitions are listed under those.

Transitions into "working" are deliberately not logged: the badge already says that, and a line per turn would bury the two that matter.

**Click a session to switch to it.** There is nothing here to read first — the panel deliberately does not render a copy of any session's screen, because the one you would want is usually the pane already sitting next to it.

The session the panel itself sits in is marked `◀`, and it is where the cursor opens — the highlight and the mark agree on where you are. There is one panel per window, so switching sessions puts you in front of a fresh one, and what it opens on is the only thing it says about where you landed. Pressing enter there is not wasted either: it hands the focus back to the pane you were working in.

**For the keyboard there are two ways in, and `mux setup-panel` installs both.**

`prefix + Tab` steps into the panel and back out again. While you are in it the panel's own keys apply — `j`/`k` to move, `enter` to go to that session, `esc` to leave without choosing. Pressing `enter` hands the focus back to the pane you came from on its way to the new session, so the round trip needs no thought.

`M-Up` / `M-Down` / `M-Enter` do the same steering **without ever moving the focus**: they reach the panel with `send-keys`, so the cursor moves while you keep typing in your own pane. That is still the panel's default mode; the focus key is for when you want to look through the list properly.

```tmux
bind Tab        run-shell "/absolute/path/to/mux panel --focus -t #{pane_id}"
bind -n M-Up    run-shell "/absolute/path/to/mux nav -t #{pane_id} up"
bind -n M-Down  run-shell "/absolute/path/to/mux nav -t #{pane_id} down"
bind -n M-Enter run-shell "/absolute/path/to/mux nav -t #{pane_id} enter"
```

`up`, `down`, `top`, `bottom` and `enter` are the directions `nav` takes. A window with no panel does nothing and exits cleanly, so both bindings are safe to make global.

Note that `q` inside the panel *closes* it rather than leaving it — `esc` is the way out. A panel closed by accident comes back on the next window or session change, or immediately with `prefix + a`.

The panel is a pane, and a pane belongs to one window — so a window that has never had one shows none. Switching sessions from the panel lands you in exactly such a window, which looks like the panel vanishing. `mux setup-panel` installs the binding and the hooks that keep one in every window you end up looking at:

```bash
mux setup-panel                 # prefix + a toggles the panel, prefix + Tab steps into it
                                # override both: mux setup-panel a Tab
tmux source-file ~/.tmux.conf
```

It writes a fenced `# mux panel { … }` block with the absolute path filled in, replaces that block on re-runs rather than stacking copies, and routes to `.tmux.conf.local` before the sentinel for [oh-my-tmux](https://github.com/gpakosz/.tmux) users. If your config ends with tpm's loader — which tpm requires to stay last — the block goes above it.

The hooks it installs are `after-new-window`, `after-new-session`, `after-select-window`, `client-attached`, `client-session-changed`, `client-resized` and `after-resize-window`. Each runs the same `mux panel --auto`. `client-session-changed` is the one that makes the panel follow you when you switch sessions; the resize pair is what brings it back to a window that had grown too narrow.

To write it yourself instead:

```tmux
bind a run-shell "/absolute/path/to/mux panel -t #{pane_id}"
set-hook -g client-session-changed 'run-shell "/absolute/path/to/mux panel --auto -t #{pane_id}"'
# … and the same line for each of the other hooks above
```

`--auto` marks the hook path. It is an *ensure* rather than a toggle — it opens a missing panel and never closes one — which is what lets the resize hooks call the same command without the panel flapping. It stands down in three cases: a window narrower than 140 columns, a session only being viewed from a VS Code integrated terminal, and a window where you closed the panel yourself. Pressing the key overrides all three, since that is a decision rather than a default, and it clears the manual-off mark so the hooks resume.

**With [tmux-resurrect](https://github.com/tmux-plugins/tmux-resurrect), a restored panel comes back as an empty shell.** The plugin saves a pane's child process, and the panel has none — it *is* the pane's process — so there is nothing for it to restore, and `@resurrect-processes` cannot help. mux marks its pane with the tmux pane title, which is the one thing a restore hands back, and closes the dead pane before opening a live one in its place. Nothing to configure; a panel that predates this is simply not recognised the first time.

**If the panel does not appear, suspect the width first** — the stand-down is silent by design, since these hooks fire constantly and an error per window would be worse. 140 is the panel's own 48 columns plus a work pane worth working in; the same number is what an open panel leaves below, so it cannot squeeze you out of a window it would not have opened in. Move the bar for your own screen:

```bash
tmux set -g @mux_panel_min_width 120
```

The width rule is how "not on a small screen" is decided. A mobile SSH client cannot be identified by environment the way VS Code can, and the real problem was never the device: with `aggressive-resize`, attaching from a phone shrinks the window to around 54 columns while the panel holds its 48, leaving the work pane five. The threshold also separates the terminals in use — VS Code's integrated terminal sits at about 150 columns, a full one at 269 — and it reaches where the environment check cannot, since a window with no client attached has nobody to inspect but still has a size.

A panel open when the window shrinks past the threshold closes itself, and the resize hooks open it again when the window grows back.

Hiding it per client is not possible — a pane belongs to a window, not a client, so a window open in both terminals shows the panel to both. The only lever is whether it exists.

`mux panel` closes the panel if the window already has one and opens it otherwise, so the key hides it as readily as it shows it and pressing twice cannot leave two. A window that was just created cannot already hold a panel, which is why the hooks call the same command rather than needing an open-only variant.

The focus stays in the pane you were working in, both when the panel opens and after you click it.

Drag the pane border to resize the panel — that still works, because tmux handles a border drag itself rather than forwarding it to the pane. The width is remembered per session (in a tmux user option, so it lives exactly as long as the session does) and the panel reopens at it. A first-time panel opens at 48 columns.

Rows are ordered by the number they print — how long the session has held its state — most recent first. Sorting by anything else makes that column non-monotonic, and a session at 41m listed under two at 3h reads as a bug. Rows do move as states change, which is why a session's block includes the blank line under it: the click target is two rows tall, not one.

Enabling clicks means tmux hands this pane every mouse event, so its own wheel-scroll into copy-mode and drag-to-select stop working *there*; hold Shift for the terminal's native selection.

It has to be a pane, not a popup: tmux has no floating window that leaves the keyboard alone. `display-popup` is explicitly documented as *"Panes are not updated while a popup is present"* — it freezes everything behind it and captures input, so it cannot be used as a glanceable overlay. A pane is the only always-visible tmux surface that still lets you type.

Use an absolute path unless `mux` is on the PATH tmux itself sees — a version-manager shim usually is not.

### Works with skimd

Pair with [skimd](https://github.com/lunemis/skimd) to review AI-generated markdown docs without leaving tmux.

- `prefix+m` → **mux** — switch sessions
- `prefix+v` → **skimd** — skim documents

![mux + skimd workflow](assets/workflow.gif)

### Keybindings

| Key | Action |
|---|---|
| `j` / `k` | Move down / up |
| Click | Move the cursor to that row and preview it |
| Double-click | Attach — same as `Enter` on that row |
| `g` / `G` | Jump to first / last |
| `Tab` / `→` / `l` | Expand session → windows → panes |
| `Shift+Tab` / `←` / `h` | Collapse one level |
| `Enter` | Attach (focuses the selected window/pane) |
| `n` | Create new session |
| `r` | Rename session |
| `x` | Delete session (with confirmation) |
| `0`–`9` | Set selected session's order (`0` clears) |
| `o` | Rotate sort: recent → name → order |
| `/` | Filter sessions by name or path |
| `Esc` | Clear filter / cancel |
| `?` | Help — marker legend and the full key list |
| `q` | Quit |

`?` opens a full-screen page explaining every marker a row can carry (`▶`/`▼`, the brighter name of the attached session, `#N`, the elapsed column, `⌥`, the AI badge) alongside the keys above — the footer bar can list a key but cannot explain a glyph. The page is written in Korean, matching `README.ko.md`.

## Requirements

- tmux (popup mode requires 3.2+)
- Linux or macOS

## Contributing

See [CONTRIBUTING.md](CONTRIBUTING.md) for guidelines.

## License

[MIT](LICENSE)
