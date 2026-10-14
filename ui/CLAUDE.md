# ui/ — 고치기 전에 읽을 설계 문서

루트 `CLAUDE.md` 의 «되돌리기 쉬운 것들»이 먼저다. 이 패키지에는 **목 러너가 없다** —
tmux·파일·프로세스를 만지는 일은 `tea.Cmd` 로, 기능 스위치는 제로값이 «꺼짐»이게.

| 파일 | 문서 (`docs/design/`) |
|---|---|
| `app.go` | tui.md, select-mode.md (틱은 루트 `CLAUDE.md`) |
| `tree.go`, `filter.go`, `preview.go`, `target.go`, `help.go`, `preferences.go`, `create.go`, `rename.go`, `confirm.go`, `order.go` | tui.md |
| `list.go` | rendering.md, ai-state.md, note.md, tui.md |
| `layout.go`, `styles.go` | rendering.md |
| `status.go` | ai-state.md |
| `note.go` | note.md |
| `source.go`, `source_herdr.go` | herdr.md |
| `source_claude.go` | claude-mode.md |
| `watch.go` | panel.md, panel-width.md |
| `watchown.go`, `notify.go`, `editor.go`, `restart.go` | panel.md (`notify.go` 는 note.md 도) |
| `watchevents.go` | panel-events.md |
| `watchghost.go` | panel-width.md |
| `border.go` | border.md |
| `remote.go` | revive.md |
| `setupoffer.go` | tmux-config.md |
