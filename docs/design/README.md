# 설계 문서 색인

mux 의 기능별 설계 이유다 — 무엇을 실측했고, 무엇을 시도했다 버렸고, 어떤 줄이 왜
load-bearing 인지. 2026-10-14 까지는 전부 루트 `CLAUDE.md` 한 파일(113KB)에 있었고, 매 세션이
그걸 통째로 읽었다. 지금은 루트에 늘 필요한 것만 두고 여기서 필요한 것만 읽는다.

| 문서 | 다루는 것 |
|---|---|
| [tui.md](tui.md) | 평탄화된 트리, 클릭/더블클릭, 프로젝트 필터 시딩, 프리뷰 대상, TUI 종료 뒤 attach, 모달 서브모델, `modeHelp`, preferences |
| [rendering.md](rendering.md) | 팔레트는 터미널의 것, 한 프레임 두 칸, 손으로 하는 고정 폭 렌더링 |
| [ai-state.md](ai-state.md) | AI 배지 하나에 두 사실, Claude 상태 파일 체인, 비용 추정 체인 |
| [note.md](note.md) | 세션 메모 `@mux_note` |
| [select-mode.md](select-mode.md) | 선택 모드(`v`)가 틱도 멈추는 이유 |
| [herdr.md](herdr.md) | `mux --herdr` — 소스 이음매, 2계층 트리, 매핑의 조용한 셋, 읽기 전용 |
| [claude-mode.md](claude-mode.md) | `mux --claude` — 대화 단위 행, 입양, `sourceActivator` |
| [pair.md](pair.md) | `-exec`/`-plan` 짝을 한 창에 합치기 |
| [bootstrap.md](bootstrap.md) | tmux 밖에서 친 `mux` 가 attach 후 팝업을 띄우는 것 |
| [panel.md](panel.md) | 패널(`mux watch`) — 위치, 커서·`ownSession`, 두 버튼(전환/에디터), 두 키보드, 세 부분, 자가 재시작, 유령 pane |
| [panel-events.md](panel-events.md) | 공유 이벤트 로그 `@mux_events`, `trimLog`, `events.json` 아카이브 |
| [panel-width.md](panel-width.md) | 패널 폭 하나, 천장·바닥, 드래그 판별, 유령 클라이언트 정리 |
| [border.md](border.md) | 작업 pane 위 테두리 줄 `mux border` |
| [revive.md](revive.md) | 소켓만 잃은 tmux 서버를 SIGUSR1 로 되살리기 |
| [tmux-config.md](tmux-config.md) | `setup-keybind`/`setup-panel` 영역, 자가 복구, 폭·VS Code 게이트, 훅, 첫 실행 제안 |

새 주제는 새 문서로 만들고, 이 표와 해당 디렉터리의 `CLAUDE.md` 에 한 줄씩 더한다.
