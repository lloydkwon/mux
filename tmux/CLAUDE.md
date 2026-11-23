# tmux/ — 고치기 전에 읽을 설계 문서

루트 `CLAUDE.md` 의 «되돌리기 쉬운 것들»이 먼저다. 모든 `tmux`/`ps`/`pgrep` 호출은
`runner` 를 거친다(예외 셋은 루트에). 목은 정확한 `"name arg1 arg2"` 로 키잉되므로 플래그를
더하면 그 테스트의 목 키도 고친다. 주석에 «measured» 를 쓰면 `scripts/tmux-assumptions.sh` 에 단언을 더한다.

| 파일 | 문서 (`docs/design/`) |
|---|---|
| `runner.go`, `process.go`, `git.go` | 루트 `CLAUDE.md` (러너, TTL 캐시) |
| `aitools.go`, `claude_status.go`, `claude_activity.go`, `claude.go` | ai-state.md |
| `claude_work.go`, `claude_work_title.go` | panel.md (세션 아래 줄), `transcript.go` 의 줄 모양은 agent-api.md |
| `proc.go` | ai-state.md, revive.md |
| `claude_sessions.go` | claude-mode.md |
| `pair.go` | pair.md |
| `park.go`, `park_opencode.go` | park.md |
| `panel.go` | panel.md, tmux-config.md (폭·VS Code 게이트) |
| `panelwidth.go`, `client.go` | panel-width.md |
| `eventlog.go`, `eventarchive.go` | panel-events.md |
| `revive.go` | revive.md |
| `popup.go` | bootstrap.md, tmux-config.md |
| `setuppanel.go` | tmux-config.md, border.md |
| `repair.go`, `setup.go` | tmux-config.md |
| `resurrect.go` | resurrect.md, panel.md (유령 패널) |
| `note.go` | note.md |
| `session.go`, `types.go`, `window.go` | tui.md (`session.go` 는 tmux-config.md 의 `SessionProjectDir` 도) |
| `language.go` | border.md (패널의 PyCharm 규칙은 panel.md) |
| `agent.go`, `agent_prompt.go`, `agent_wait.go`, `transcript.go` | agent-api.md (상태 단어는 ai-state.md 와 대조). `ErrAgentBusy`·`ReadLastTurn` 은 serve.md 도 |
| `agent_dialog.go` (Claude 의 질문·권한 확인 대화상자 읽고 답하기, 실측 화면은 `testdata/dialog`) | serve.md «대화상자에 답하기» |
| `viewport.go`, `vscodewindows.go` | 설계 문서 없음 — 파일 머리 주석이 정본 |
