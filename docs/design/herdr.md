# `mux --herdr` — herdr 를 목록의 출처로

> 관련 코드: herdr/*, ui/source.go, ui/source_herdr.go
>
> 원래 루트 `CLAUDE.md` 의 절이었다(2026-10-14 분리, 본문은 그대로). 본문의 «이 파일»·«위/아래 절»은 `docs/design/` 의 다른 문서일 수 있다 — 색인은 `docs/design/README.md`.

## `mux --herdr` 는 목록의 출처를 바꾼다

작업 환경이 tmux 에서 **herdr**(터미널 워크스페이스 매니저)로 옮겨가면 mux 의 전제가
통째로 없어진다 — tmux 서버가 안 떠 있으면 목록은 액션 행 두 개뿐이다. `--herdr` 는
목록을 herdr 가 채우게 한다. **읽기 전용이고, 플래그가 없으면 아무것도 변하지 않는다.**

`herdr` CLI 를 shell out 한다. 소켓 직결(`$HERDR_SOCKET_PATH`, 줄 단위 JSON
`{id,method,params}`)이 실측 1.1ms 로 CLI 의 2~3ms 보다 빠르지만, 500ms 틱에서 그
차이는 duty cycle 2% 안쪽이고 소켓은 **저장소의 목 러너 패턴을 쓸 수 없다**. 프로토콜의
두 번째 구현체를 herdr 버전마다 맞춰야 하는 것도 같이 온다. mux 가 tmux 의
control-mode 가 아니라 `tmux` 바이너리를 부르는 것과 같은 판단이다.

**`herdr/runner.go` 는 `tmux/runner.go` 의 형태만 복제하고 인스턴스는 분리한다.** 목이
정확한 `"name arg1 arg2"` 문자열로 키잉되므로 한 맵을 공유하면 두 패키지의 기대 키가
충돌한다.

**`Snapshot()` 이 세 목록을 한 번에 가져오고 TTL 400ms 로 캐시한다.** 워크스페이스의
디렉터리와 나이가 pane 에서 나오므로, 다른 순간에 읽은 pane 목록과 짝지으면 존재한 적
없는 배치를 그리게 된다. TTL 이 틱보다 짧아 매 틱 갱신되고, 한 틱 안에서 펼쳐진 세션마다
일어나는 `Windows()` 팬아웃은 전부 흡수한다 — **워크스페이스가 몇 개든 틱당 exec 는 3회**다.

**`herdr pane read` 만 `call()` 을 우회한다.** 실측: 이 명령만 JSON 봉투가 아니라 pane 의
화면 텍스트를 그대로 stdout 에 낸다(봉투는 소켓에만 있고, 텍스트는 `result.read.text`).
JSON 으로 파싱하면 사용자가 돌리고 있는 것의 첫 줄에서 깨진다.

### 이음매는 `ui/source.go` 의 인터페이스 하나이고 **nil 이 tmux 다**

`Model.src` 가 nil 이면 `tmuxSource{}` 로 풀린다. 그래서 `Model{...}` 을 손으로 만드는
기존 테스트 전부가 한 줄도 안 바뀌고 통과한다 — `exePath` 가 패널의 자가 재시작을,
`editorBin` 이 오른쪽 클릭을 끄는 것과 같은 규칙이고, 이게 `go test ./ui` 를 개발자의
실제 서버에서 떼어 놓는다.

함수 변수 넷이 아니라 인터페이스 하나인 이유는 제로값 검사 자리다. 넷이면 검사가 네
군데로 흩어지고, 하나를 빠뜨리면 herdr 모드가 tmux 를 부른다 — tmux 서버가 없는
기계에서 그건 조용히 빈 목록이라 원인을 가장 찾기 어렵다.

패키지 레벨 `loadSessions` 는 남아 있다. `ui/watch.go` 가 값으로 넘기고, 패널은 tmux
pane 이라 herdr 모드를 가질 일이 없다.

`NewHerdrModel` 이 `NewModel` 을 거치지 않는 것도 지름길이 아니다. `NewModel` 은 tmux 의
`@project_dir` 로 `filterText` 를 시딩하는데, herdr 모드에서 그건 낭비되는 tmux 왕복이자
tmux 서버가 있는 기계에서는 어떤 워크스페이스와도 안 맞는 필터다 — 목록이 빈 채로 열려
"herdr 가 안 붙었다" 로 읽힌다.

### 트리는 워크스페이스 > agent 2계층이고, `ui/tree.go` 는 한 줄도 안 바뀐다

herdr 는 workspace > tab > pane 3계층이지만 실측한 워크스페이스가 전부 `tab_count: 1`
이고 `label` 이 `"1"` 이라, 남겨 두면 워크스페이스마다 빈 행이 하나씩 영구히 끼고 agent 에
닿는 데 `tab` 을 두 번 눌러야 한다. **`pane_id` 채번이 워크스페이스 스코프**(`w8:p1`,
`w8:p2` 가 한 탭 안)라서 탭을 몰라도 pane 을 지목할 수 있다 — 그게 탭을 버릴 수 있었던
근거다. `flattenMenu`, `treeState`, `pruneCaches`, `previewKeyForItem`, 마우스 맵이 전부
무영향이다.

**대가는 `tmux.Window` 에 AI 상태 네 필드가 붙는 것이다.** `Session` 은 세션당 상태
하나를 드는데 그건 세션을 대표하는 행에는 맞고 agent 하나를 대표하는 행에는 틀리다.
tmux 경로는 `ListWindows` 가 그 필드를 하나도 안 채우므로 `formatWindowRow` 의 가드가
조기 반환하고 **그리는 결과가 바이트 그대로**다 —
`TestWindowRowWithoutAIStateIsUnchanged` 가 그걸 지킨다. 무조건 그리는 컬럼으로 바꾸는
것이 여기서 가장 하기 쉬운 실수다.

배지 판단은 `aiGlyph` 를 그대로 통과시킨다(빌린 `Session` 을 넘긴다). 상태 글리프가 도구
아이콘을 대체하는 규칙을 정하는 곳이 하나라는 것이 [ai-state.md](ai-state.md) 에 적혀 있고, 두
번째 사본이 조용히 갈라지는 것보다 낫다. 그리고 agent 행의 나이·배지는 **세션 행과 같은
컬럼**에 찍힌다(`windowLabelWidth`) — 라벨이 끝난 자리에 붙이면 행마다 배지를 찾아야
하는데, 목록이 표이고 컬럼을 렌더당 한 번 정하는 이유가 정확히 그거다.

목록은 **펼쳐진 채로 연다**(`treeState.expandNew`). 그 아래 행들이 이 목록을 여는
이유인데 접힌 셰브런 여섯 개는 보러 온 것을 보려고 `tab` 을 여섯 번 누르라는 말이다.
`everSeen` 으로 «처음 본 것만» 펼치므로 사용자가 접은 것은 접힌 채로 남는다.
`seedWindows` 는 소스가 세션 목록과 함께 보낸 창을 캐시에 넣는다 — tmux 는
`Session.Windows` 를 아무도 안 채우므로 그쪽에선 no-op 이고, herdr 는 왕복 한 번과
«펼쳐진 첫 프레임이 비어 있는 한 틱»을 같이 없앤다.

### 매핑에서 조용한 것 셋

**`Created` 를 합성한다.** herdr 에는 워크스페이스 생성 시각이 응답에도 스키마에도
없다(`scripts/herdr-assumptions.sh` 3절이 그게 여전히 없음을 확인한다). 비워 두면
`compactAgo` 가 `time.Since(1년)` 을 `"740049d"` 로 그린다 — 4칸 컬럼에 7칸이고
`timeAgo` 의 `%4s` 는 패딩만 하고 자르지 않으므로 뒤 컬럼이 전부 밀린다. 그 워크스페이스의
가장 최근 상태 변경을 쓰면 "여기서 뭔가 일어난 지 얼마" 가 되어 오히려 더 읽힌다.

**단, 아무것도 안 도는 워크스페이스는 제로를 받는다.** 처음에는 스냅샷 시각을 넣었는데
그게 답이 없는 것보다 나빴다 — 그 워크스페이스 하나가 `"0s"` 로 최근순 목록 맨 위에
올라와, 할 말이 없다는 이유로 가장 신선한 척을 했다. 제로면 두 소비자가 이미 옳게
처리한다: `compactAgo` 는 빈칸을 그리고(그 제로 가드가 `ui/list.go` 에 있다) 최근순은
맨 뒤로 보낸다.

**`idle` 은 `AIStateNone` 이다.** herdr 문서상 `idle` 과 `done` 은 둘 다 입력 대기지만
`done` 만 "아직 안 본 완료" 이므로 `AIStateReady` 의 정의("the turn finished and the tool
awaits input")와 글자 그대로 같다. `idle` 도 Ready 로 보내면 실측 아홉 중 다섯이 ✅ 를
달아 배지가 아무것도 구분하지 못한다 — Claude 의 sticky `shell` 상태에서 [ai-state.md](ai-state.md) 가 이미
기록한 실패다. `None` 이어도 행이 비지 않는다: `SessionAITool` 이 `AITool` 로 답하고
`aiGlyph` 가 상태 글리프가 없을 때 도구 아이콘으로 폴백한다.

**셸의 터미널 제목은 작업 이름이 아니다.** 셸도 제목을 쓰므로, 그걸 그대로 읽으니
Claude 가 턴에 붙인 이름이 들어갈 칸에 `lloydkwon@DESKTOP:~/dev/...` 가 들어가고 행
이름까지 그걸로 잡아 컬럼을 통째로 먹었다. 제목은 `agent` 가 있는 pane 에서만 본다
(`taskOf`).

### 읽기 전용은 화면에 말한다

가드는 `activateCurrent` 한 곳이다 — `enter` 와 더블클릭이 이 함수를 공유하게 만든
설계의 배당금이고, `cmd/mux` 에 두 번째 가드를 두는 것이 이 함수가 막으려는 바로 그
drift 다. `n`/`x`/`r`/`m` 은 **모드 진입 자체**를 막는다(서브모델 안에서 나중에 거절하면
사용자가 이름을 다 치고 나서 안 된다는 말을 듣는다). 액션 행 둘은 `rebuildItems` 에서
빠진다.

`extraBar` 에 **상시** 한 줄이 있다. 에러보다 아래인 것은 소스가 죽었을 때가 사용자가
손쓸 수 있는 쪽이기 때문이고, 키마다 메시지를 띄우지 않는 이유는 두 가지다 — 아무 말
없이 안 되는 키는 고장난 프로그램과 구분되지 않고, 나타났다 사라지는 바는 `chromeTop` 을
1↔2 로 흔들어 클릭 좌표를 한 행씩 옮긴다(선택 모드 바가 켜져 있는 동안 상시인 것과 같은
이유). 도움말 바는 아직 되는 키만 남긴다. 제목은 `herdr workspaces` 로 바뀌지만
**글리프는 그대로다** — 새 글리프는 `TestGlyphWidthsAreStable` 의 대상이고 `ui/layout.go`
는 폭 보정이 안 된다.

실패는 새로 만들 게 없다. 소켓이 없거나 herdr 가 안 떠 있으면 `Sessions()` 가 에러를
돌리고 `m.err` → `extraBar` 의 빨간 줄로 간다 — tmux 바이너리가 없을 때 이미 도는
경로다. **`ListSessions` 의 "exit status → `(nil,nil)` 삼키기" 는 복제하지 않는다.** 그
규칙은 "tmux 서버 없음"이 세션 매니저에게 정상 상태라서 있는 것인데, `--herdr` 는
사용자가 명령줄에서 이름을 대고 요구한 것이라 닿지 않으면 그렇다고 말해야 한다.

### 지금 없는 것

토큰 사용량·비용 추정과 `waitingFor` 상세는 범위 밖이다. 붙일 때를 위해:
`agent_session.value` 가 Claude 세션 UUID 이고 `cwd` 가 같이 오므로
`tmux.LoadTokenUsage(sessionID, cwd)` 에 곧장 넣을 수 있다 — `pgrep` 체인
`FindClaudeSession` 이 통째로 불필요하다. 다만 **같은 UUID 를 가진 죽은 상태 파일이 실제로
있으므로**(`--resume` 잔재, `~/.claude/sessions/` 에서 실측) `tmux/proc.go` 의
`processAlive` 필터가 필수이고, `agent_session.kind` 가 `"path"` 면 값이 UUID 가 아니라
트랜스크립트 경로라 `"id"` 일 때만 써야 한다.

herdr 로 **전환**(`workspace.focus`)도 없다. 열고 싶어지면 `activateCurrent` 의 가드가 그
자리다.

`agent 행의 ▶ 를 눌러도 아무것도 안 나온다`는 알려진 흠이고 수용한 것이다 — herdr 모드에
pane 계층이 없다. 빈 tmux 창을 펼쳤을 때 오늘 이미 일어나는 일과 같다.
