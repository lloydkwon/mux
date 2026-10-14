# cmd/mux/ — 고치기 전에 읽을 설계 문서

`main.go` 는 TUI 밖에서 일어나야 하는 것들의 자리다(attach, 세션 생성, 입양, 되살리기, 훅 경로).

| 함수 | 문서 (`docs/design/`) |
|---|---|
| `runRoot`, `runTUI` (attach·팝업 bootstrap·detach) | bootstrap.md, tui.md |
| `adoptClaudeSessions` | claude-mode.md |
| `reviveAndRemember` | revive.md |
| `runPanelAuto` | tmux-config.md |
| `runBorder` | border.md |
| 설정 복구 호출 | tmux-config.md |
