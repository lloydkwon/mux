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
- **An always-on sidebar** — `mux watch` keeps a pane on the left of the window
  with every session and a log of what they just finished. Pick one and its live
  output appears in the column beside the list; pick it again to actually go
  there. Drive it with the mouse, or with `mux nav` from a tmux binding, which
  moves the selection without taking the keyboard away from the pane you are
  typing in.

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

The list shows what each session is doing *now*, but not what it just finished. `mux watch` fills a dedicated pane on the left of the window with your sessions, a log of recent state changes, and the live output of whichever session you pick — so it is on screen while you work rather than only while the TUI is open:

```
 🔔 AI 세션                      │  api-server                          ⌥ feat/auth
                                 │  ❗ 승인 대기 · permission prompt  12s
 ❗ api-server 12s   ⌥ feat/auth │ ────────────────────────────────────────────────
    permission prompt            │ > npm test
                                 │ ✓ 42 passed
 ⏳ mux 3m                ⌥ main │
                                 │ ❯ Allow edit to config.ts?
 ✅ web 2h             ⌥ release │   1. Yes
                                 │   2. No, tell Claude what to do differently
 ── 세션                         │
                                 │ ────────────────────────────────────────────────
    notes 3h              ⌥ main │  13:00:47 api-server ❗ 승인 대기 · permission...
                                 │  12:59:47 web ✅ 작업 완료
```

Sessions running an AI CLI come first; the rest sit under `── 세션` so every session is reachable without opening the TUI.

Transitions into "working" are deliberately not logged: the badge already says that, and a line per turn would bury the two that matter.

**Click a session to read it, click it again to go there.** The first click only points the right column at it, which is the point — finding out what another session is asking should not cost you the pane you are typing in. A pane too narrow to split (below 76 columns) shows the list alone, and there a single click switches, since there is nothing to read first.

The session the panel itself sits in is marked `◀` and is skipped when the panel picks a row for you. Its output is already on screen in the pane beside the panel, and it is nearly always the top row — the list is ordered by how recently a state changed, and the session you are working in is the one whose state keeps changing — so left alone the column would open showing a copy of the pane next to it. Selecting it deliberately still works, and gives a summary instead of a mirror: where it is, how many windows, how long it has been up, and what it has cost.

For the keyboard, bind `mux nav`. It reaches the panel with `send-keys`, so the selection moves without the focus leaving your own pane:

```tmux
bind -n M-Up    run-shell "/absolute/path/to/mux nav -t #{pane_id} up"
bind -n M-Down  run-shell "/absolute/path/to/mux nav -t #{pane_id} down"
bind -n M-Enter run-shell "/absolute/path/to/mux nav -t #{pane_id} enter"
```

`up`, `down`, `top`, `bottom` and `enter` are the directions it takes. A window with no panel does nothing and exits cleanly, so the binding is safe to make global.

```tmux
# Add to ~/.tmux.conf — prefix+a toggles the panel in the current window
bind a run-shell "/absolute/path/to/mux panel -t #{pane_id}"

# And put it in every new window and session automatically
set-hook -g after-new-window  'run-shell "/absolute/path/to/mux panel --auto -t #{pane_id}"'
set-hook -g after-new-session 'run-shell "/absolute/path/to/mux panel --auto -t #{pane_id}"'

# And bring it back when a window that was too narrow grows again
set-hook -g client-resized      'run-shell "/absolute/path/to/mux panel --auto -t #{pane_id}"'
set-hook -g after-resize-window 'run-shell "/absolute/path/to/mux panel --auto -t #{pane_id}"'
```

`--auto` marks the hook path. It is an *ensure* rather than a toggle — it opens a missing panel and never closes one — which is what lets the resize hooks call the same command without the panel flapping. It stands down in three cases: a window narrower than 200 columns, a session only being viewed from a VS Code integrated terminal, and a window where you closed the panel yourself. Pressing the key overrides all three, since that is a decision rather than a default, and it clears the manual-off mark so the hooks resume.

The width rule is how "not on a small screen" is decided. A mobile SSH client cannot be identified by environment the way VS Code can, and the real problem was never the device: with `aggressive-resize`, attaching from a phone shrinks the window to around 54 columns while the panel holds its 48, leaving the work pane five. The threshold also separates the terminals in use — VS Code's integrated terminal sits at about 150 columns, a full one at 269 — and it reaches where the environment check cannot, since a window with no client attached has nobody to inspect but still has a size.

A panel open when the window shrinks past the threshold closes itself, and the resize hooks open it again when the window grows back.

Hiding it per client is not possible — a pane belongs to a window, not a client, so a window open in both terminals shows the panel to both. The only lever is whether it exists.

`mux panel` closes the panel if the window already has one and opens it otherwise, so the key hides it as readily as it shows it and pressing twice cannot leave two. A window that was just created cannot already hold a panel, which is why the hooks call the same command rather than needing an open-only variant.

The focus stays in the pane you were working in, both when the panel opens and after you click it.

Drag the pane border to resize the panel — that still works, because tmux handles a border drag itself rather than forwarding it to the pane. The width is remembered per session (in a tmux user option, so it lives exactly as long as the session does) and the panel reopens at it. A first-time panel opens at 84 columns on a window with room to spare, which is enough for both columns, and at 48 on one that has not.

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

`?` opens a full-screen page explaining every marker a row can carry (`▶`/`▼`, `*`/`○`, `#N`, the elapsed column, `⌥`, the AI badge) alongside the keys above — the footer bar can list a key but cannot explain a glyph. The page is written in Korean, matching `README.ko.md`.

## Requirements

- tmux (popup mode requires 3.2+)
- Linux or macOS

## Contributing

See [CONTRIBUTING.md](CONTRIBUTING.md) for guidelines.

## License

[MIT](LICENSE)
