# 작업 pane 위의 한 줄 (`mux border`)

> 관련 코드: ui/border.go, cmd/mux (runBorder), tmux/setuppanel.go
>
> 원래 루트 `CLAUDE.md` 의 절이었다(2026-10-14 분리, 본문은 그대로). 본문의 «이 파일»·«위/아래 절»은 `docs/design/` 의 다른 문서일 수 있다 — 색인은 `docs/design/README.md`.

## The line above the pane you work in

`pane-border-status top` plus a `pane-border-format` that calls `mux border` (`ui/border.go`, `cmd/mux`'s `runBorder`, installed by `SetupPanel`) puts the session's summary — name, directory, tool, live state, branch — on the *other* column, above the shell. It is the only row of that side mux gets to write in: the pane is the user's terminal, and its title already belongs to whatever runs there (Claude Code names its pane after the task in hand, so taking it over would cost more than the line gives).

`BorderLine` breaks three of this codebase's own rules on purpose. It emits **no ANSI**, because tmux styles formats with `#[fg=…]` and paints raw escapes as text — colour is left to `pane-border-style`, and the state glyph carries what a colour would have said. It **does not right-align**, because tmux fills what the format leaves unused with `─`, so padding to the full width would replace that run with blanks and break the border. And it **gives up its parts from the right** — branch, then the tool's name, then the directory — never the session name, since a border that cannot say which session it belongs to has nothing worth drawing.

Two things were measured rather than assumed. `#()` **does** re-run inside `pane-border-format` (verified on an isolated server: renaming a session changed the line without a restart), on the `status-interval` cadence — the manual only documents this for the status line. And the border **costs one row per pane**, the panel's included: measured 20 → 19 for one pane, 20 → 18 with a second work pane.

The format's conditional skips the panel's own pane by matching `panelCommand` against `pane_start_command`. Deciding it in tmux rather than in `mux border` means the panel spawns no process at all, and `mux border` never has to work out which pane it is being asked about. **The line carries two levels of quoting for two parsers**: double quotes for tmux (which is what stops `#` in `#{pane_id}` starting a comment) and single quotes around the path inside `#()`, which `/bin/sh` reads — `TestPanelBlockQuotingKeepsPaneIDInsideQuotes` pins both.

`mux border` is a fresh process per pane per refresh, which is why it uses `tmux.SessionForTarget` rather than `ListSessions`: one `display-message` with list-sessions' own format, handed to the same `parseLine`. It prints an empty line and exits 0 on any failure — an error string here is not reported anywhere a user can act on it, it is simply painted across the top of the pane they are working in and stays there.
