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
go test ./herdr                        # herdr 매핑 — 캡처한 픽스처로 hermetic
go vet ./... && golangci-lint run       # expected before submitting (no repo config; defaults)
GOARCH=arm64 go build ./cmd/mux         # CI also cross-compiles this

scripts/test-fixture.sh up      # real tmux sessions (multi-window/pane/empty) for manual TUI checks
scripts/test-fixture.sh down    # tear them down

make herdr-check                # herdr 의 응답 모양이 그대로인지 (herdr 안에서, 읽기만)
./mux --herdr                   # herdr 를 데이터 출처로 목록을 연다 (읽기 전용)
./mux --claude                  # 돌고 있는 클로드 «대화» 목록; enter 로 가거나 여기서 이어간다
```

CI (`.github/workflows/ci.yml`) runs test + build + arm64 cross-build + `scripts/tmux-assumptions.sh`.

**`scripts/tmux-assumptions.sh` is the only thing that tests tmux itself.** Every unit test mocks the runner, which is what makes them fast and hermetic — and also means tmux can change underneath mux without a single test failing. The script starts a private server (`-L`, `-f /dev/null`, so the developer's own sessions and hooks are never touched) and asserts the behaviours the code depends on: the `tmux -V` string `getTmuxVersion` parses, `listFormat`'s field count, that `#{@user_option}` resolves a session option through a pane target, that `-l N` on `split-window` gives exactly N columns, that `list-panes` remembers `pane_start_command` (the panel's only marker), that a batched `capture-pane ; display-message ; capture-pane` emits one separator, and that `run-shell` and `after-new-session` expand `#{pane_id}`. **Add a line here whenever a comment says "measured" or "verified"** — otherwise that one assumption is the one that breaks silently on the next upgrade. `make tmux-check` runs it; `TMUX_BIN=/path/to/tmux` points it at a candidate binary instead of PATH.

**Check the candidate before switching, not after.** Putting a new tmux on PATH makes the *client* new while the running *server* stays old, and the protocol mismatch rejects every tmux call — mux, its panels and any command you type — until the server restarts, which ends every session on it. `TMUX_BIN` is how the answer arrives before that price is paid. Verified this way: 22/22 on 3.4 and on 3.7c.

Note the private server has no user config, so `base-index` is tmux's default 0 — target windows by `#{window_id}`, never `sess:1`. Releases are tag-driven via goreleaser. Commits follow Conventional Commits.

## Architecture

Three layers, strictly one-directional:

- `cmd/mux` — cobra CLI: root (TUI), `popup`, `setup-keybind`, `setup-panel`, `status`, `watch`, `panel`, `nav`.
- `tmux/` — everything that shells out or reads the filesystem. No UI dependencies.
- `herdr/` — the same job for herdr, and the only package that reads it. Imports `tmux` for the session types and nothing else; `tmux` does not know it exists.
- `ui/` — Bubble Tea model, rendering, preferences. Depends on `tmux` and `herdr`.

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

### `mux --herdr` 는 목록의 출처를 바꾼다

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

#### 이음매는 `ui/source.go` 의 인터페이스 하나이고 **nil 이 tmux 다**

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

#### 트리는 워크스페이스 > agent 2계층이고, `ui/tree.go` 는 한 줄도 안 바뀐다

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
아이콘을 대체하는 규칙을 정하는 곳이 하나라는 것이 이 파일의 다른 절에 적혀 있고, 두
번째 사본이 조용히 갈라지는 것보다 낫다. 그리고 agent 행의 나이·배지는 **세션 행과 같은
컬럼**에 찍힌다(`windowLabelWidth`) — 라벨이 끝난 자리에 붙이면 행마다 배지를 찾아야
하는데, 목록이 표이고 컬럼을 렌더당 한 번 정하는 이유가 정확히 그거다.

목록은 **펼쳐진 채로 연다**(`treeState.expandNew`). 그 아래 행들이 이 목록을 여는
이유인데 접힌 셰브런 여섯 개는 보러 온 것을 보려고 `tab` 을 여섯 번 누르라는 말이다.
`everSeen` 으로 «처음 본 것만» 펼치므로 사용자가 접은 것은 접힌 채로 남는다.
`seedWindows` 는 소스가 세션 목록과 함께 보낸 창을 캐시에 넣는다 — tmux 는
`Session.Windows` 를 아무도 안 채우므로 그쪽에선 no-op 이고, herdr 는 왕복 한 번과
«펼쳐진 첫 프레임이 비어 있는 한 틱»을 같이 없앤다.

#### 매핑에서 조용한 것 셋

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
달아 배지가 아무것도 구분하지 못한다 — Claude 의 sticky `shell` 상태에서 이 파일이 이미
기록한 실패다. `None` 이어도 행이 비지 않는다: `SessionAITool` 이 `AITool` 로 답하고
`aiGlyph` 가 상태 글리프가 없을 때 도구 아이콘으로 폴백한다.

**셸의 터미널 제목은 작업 이름이 아니다.** 셸도 제목을 쓰므로, 그걸 그대로 읽으니
Claude 가 턴에 붙인 이름이 들어갈 칸에 `lloydkwon@DESKTOP:~/dev/...` 가 들어가고 행
이름까지 그걸로 잡아 컬럼을 통째로 먹었다. 제목은 `agent` 가 있는 pane 에서만 본다
(`taskOf`).

#### 읽기 전용은 화면에 말한다

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

#### 지금 없는 것

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

### `mux --claude` 는 대화를 세운다

`--herdr` 와 `--claude` 는 같은 이음매를 쓰지만 세는 단위가 다르다. herdr 모드의 행은
워크스페이스 — 터미널이다. **claude 모드의 행은 대화다**: transcript 하나와, 때로는
그것을 들고 있는 프로세스 하나. 그래서 행이 터미널보다 오래 살고, 그게 이 모드가
있는 이유다 — 작업을 다른 데서 이어받을 수 있다.

`tmux/claude_sessions.go` 가 `~/.claude/sessions/*.json` 을 읽는다. `claude_status.go`
가 같은 디렉터리를 읽으면서 **`tmux` 필드 없는 파일을 전부 버리는** 것과 대비된다 —
거기서는 맞다, 배지를 얹을 tmux 행이 없으니까. 실측: 살아 있는 클로드 12개 중 3개만
tmux 안이었고, 그 필터가 돌고 있는 것의 4분의 3 을 떨어뜨리고 있었다.

**한 대화는 한 행이다.** 같은 `sessionId` 가 herdr pane 과 tmux pane 에서 «동시에» 살아
있는 것을 실제로 두 건 관측했다(사용자가 손으로 resume 한 결과). `preferRun` 은 tmux
쪽을 남긴다 — enter 가 아무것도 시작하지 않고 갈 수 있는 쪽이기 때문이다. 동률이면 더
최근에 움직인 쪽.

**행 이름은 사용자가 붙인 이름이라도 그대로 쓴다.** 패널의 `claudeTaskName` 은 사용자
이름을 버리는데(고정된 라벨이 모든 전이 로그에 반복되면 아무 말도 안 하므로), 여기서는
그 이름이 곧 **행의 정체**다 — 어느 대화인지 사람이 알아보는 유일한 단서다. 실측 12개
중 9개가 사용자 이름이었다. 대신 `Session.Name` 은 확장 상태·순서 맵·커서가 모두 키로
쓰므로 충돌하는 것에만 짧은 sessionId 를 붙인다(`uniqueRunNames`).

**`Attached` 는 여기서 «tmux 로 갈 수 있다»는 뜻이다.** 목록에서 이름이 accent 색으로
뜨는 것이 늘 «아무것도 시작하지 않고 갈 수 있는 행»을 뜻해 왔으므로, 컬럼을 하나도 안
쓰고 enter 가 «가기»인지 «시작하기»인지 말해 준다.

#### 켜질 때 집 없는 대화를 전부 입양한다

`--claude` 는 시작할 때 **tmux 세션이 없는 대화에 전부 세션을 만든다**
(`AdoptClaudeSessions`, `tmux/claude_sessions.go`). 행마다 `enter` 를 눌러야 했던 것을
뒤집은 것이고, 목적은 herdr 등에 흩어진 작업을 mux 안으로 들여와 **평범한 `mux` 목록도
같이 완성되게** 하는 것이다.

**`--fork-session` 을 쓰지 않는 것이 이 기능이 폭주하지 않는 유일한 이유다.** 포크한
자식은 새 `sessionId` 를 받고 **상태 파일 어디에도 부모를 가리키는 필드가 없다**(전체 키를
확인했다). 그래서 포크로 입양하면 원본은 다음 실행에도 여전히 «tmux 밖»이라 또 만들고,
`wai-plan-2`, `wai-plan-3` … 이 끝없이 늘어난다. 실제로 그 형상을 현장에서 봤다 — 손으로
enter 를 눌러 만든 포크 세션 옆에 원본이 그대로 남아 있었다. 같은 id 로 `--resume` 하면
새 프로세스가 같은 `sessionId` 로 상태 파일을 쓰므로 다음 스캔에서 `preferRun` 이 둘을 한
행으로 접고 tmux 쪽을 고른다. **디스크에 아무것도 기억하지 않아도 두 번 입양되지 않는다.**

**그 대신 `AdoptableRuns` 는 «끝난 턴»만 고른다.** 같은 transcript 을 두 프로세스가
들고 있어도 괜찮은 근거는 하나뿐이다 — 버려진 쪽이 아무것도 쓰지 않는다는 것. 실측:
두 대화가 herdr pane 과 tmux pane 에서 몇 시간 동안 동시에 살아 있었고 transcript 은
파싱 실패 0 이었다. `AIStateWorking` 은 남의 턴 한가운데고, `Approval` 은 그쪽이 물어
놓고 기다리는 중이며, `Shell` 은 배경 명령이 살아 있다는 뜻이다. 셋 다 건드리지 않는다 —
**취향이 아니라 위 근거의 전제다.** 수동 `enter` 는 그 셋도 커밋할 수 있고, 거기서만
`ClaudeBusyElsewhere` 가 참이 되어 포크한다.

**생성은 순차이고, 한 건마다 벽시계 초가 바뀌기를 기다린다.** 둘 다 강제다.
순차인 이유는 `freeSessionName` 이 호출마다 `list-sessions` 를 다시 읽고 예약을 갖지
않아서다 — 병렬이면 같은 스냅샷을 보고 같은 이름을 골라 뒤엣것이 `duplicate session` 으로
죽는다. **초를 기다리는 이유는 세션 생성 «뒤에» 도는 것 때문이다**: tmux 가
`session-created` 를 쏘고, 이 사용자의 설정에서는 그것이 tmux-resurrect 저장을 부른다.
그 저장본은 파일명이 초 단위이고 쓰기가 원자적이지 않다(truncate 한 번 + append 두 번).
한 초에 여러 개가 겹치면 한 파일에 섞여 들어가고, 사용자의 `.tmux.conf` 주석이 그 사고를
이미 실측해 뒀다 — pane 줄의 절반이 중복되고, 그 파일로 복원하면 pane 이 두 배가 된다.
초를 넘겨 주면 저장본마다 이름이 달라지고, **mux 는 그 플러그인에 대해 아무것도 몰라도
된다**(`save.sh` 를 직접 부르는 대안을 버린 이유다).
실측 확인: 7건이 6초에 끝났고 저장본 7개가 초마다 하나씩, pane 줄 중복 0.

대가는 대화 하나당 1초이고 **한 번뿐이다** — 자기 제한적이라 다음 실행은 아무것도 만들지
않는다. 그래서 `cmd/mux` 의 `adoptClaudeSessions` 는 만들 게 없으면 **한 마디도 하지
않고**, 있을 때만 건별로 한 줄을 찍는다. 설명 없는 6초는 멈춘 것으로 읽힌다.

**`ui` 가 아니라 `cmd/mux` 에서, `runTUI` 앞에 돈다.** 이유가 셋이고 하나만으로도
충분하다: 순차 실행이 평범한 루프로는 공짜지만 `tea.Batch` 로는 불가능하고, `ui` 에는
mock runner 가 없어 Update 에서 tmux 를 만지면 `go test ./ui` 가 개발자의 서버에 세션을
만들며, `Model` 의 resume 슬롯은 하나뿐이라 배치를 실을 수 없다.

**끝나고 `ResetClaudeRunCache()` 를 부른다.** 스캔 캐시가 1초인데 세션은 그 안에 생기므로,
안 비우면 TUI 첫 로드가 «생기기 전»의 스냅샷을 보고 같은 대화를 또 집 없는 것으로 그린다.

**갓 만든 세션에도 패널이 붙는다.** 처음에는 안 붙는다고 적었는데 틀렸다 — 격리 서버(클라이언트
없음)에서 잰 80x24 를 보고 폭 게이트에 걸린다고 판단했지만, 클라이언트가 붙어 있는 실제
서버에서는 새 창이 그 클라이언트 크기(269)를 물려받아 게이트를 통과한다. 세션마다 패널이
하나씩 생기는 것은 mux 의 평소 동작이므로 결함은 아니지만, **7개를 입양하면 `mux watch`
프로세스도 7개 늘어난다**는 것은 알고 있어야 한다.

#### enter 의 뜻이 소스마다 다르다 — `sourceActivator`

`sessionSource` 에 얹지 않고 선택 구현 인터페이스로 뺐다. 세 소스의 답이 서로 무관하기
때문이다: 목록만 하는 소스는 커밋에 답이 없고(herdr), 행이 mux 가 닿는 곳에 있는
소스는 «어디»라고 답하고(tmux), 행이 아직 아무 데도 없을 수 있는 소스는 «어떻게
시작하는지»를 답한다(claude).

- tmux 가 들고 있으면 → **간다.** 아무것도 시작하지 않는다. 세션까지만 지목하는데,
  `AttachToSession` 은 창·pane 을 인덱스로 다루고 Claude 는 id(`@2`, `%4`)로 기록하기
  때문이다 — 대화를 든 pane 은 억지 상황이 아니면 이미 active 다.
- 아니면 → **여기서 시작한다.** 기본은 **같은 id 로 `--resume`** 이다(위 절 참조).
  예외는 `ClaudeBusyElsewhere` — 다른 데서 «지금 작업 중»인 대화뿐이고, 거기서만
  포크한다. 살아 있다는 것만으로는 부족하다: 위험한 것은 살아 있는 사본이 아니라
  «쓰고 있는» 사본이다.

세션을 만드는 것과 붙는 것은 둘 다 **Bubble Tea 루프 밖**에서 일어난다. `Update` 는
`resumeTarget` 을 세우고 `tea.Quit` 할 뿐이고, `cmd/mux` 가 그것을 본다 — `attachTarget`
이 늘 그래 왔던 것과 같은 이유다.

**오른쪽 칸을 비워 두는 것이 첫 버전이었고 틀렸다.** 열 중 일곱이 tmux 밖이라 목록이
설명하는 칸이 대부분 비어 있었고, 빈 칸은 «다른 데 있음»이 아니라 고장으로 읽힌다.
커밋하기 전에 읽는 칸이므로 `elsewhereNotice` 가 거기에 enter 의 결과를 적는다 —
포크한다는 것과 왜 그러는지까지. 행만 보고 추론할 수 있는 내용이 아니다.

**쓰기 키를 막는 기준은 `readOnly` 가 아니라 `writesBlocked()`(= 소스가 설정됨)다.**
생성·종료·이름변경·메모는 전부 tmux 세션에 대한 동작인데, 행이 다른 것을 뜻하면 대상이
없다. enter 가 되느냐는 다른 질문이고, 그래서 검사도 따로다.

**제목과 안내 줄은 소스가 직접 말한다**(`sourceChrome`). 목록이 아래에서는 herdr 라고
하면서 바로 위 제목은 `tmux sessions` 라고 하던 것이 그 둘이 갈라져 있었기 때문이다.

**`CreateSessionWithCommand` 은 `list-sessions` 를 한 번 부른다.** 후보마다
`has-session` 을 치는 것이 처음이었고, 그건 최대 98번의 왕복인 것보다 «답할 수 없는
tmux»와 «비어 있는 이름»이 구분되지 않는 것이 문제다. 목록을 한 번 읽으면 «서버 없음»이
«세션 없음»이 되는데, 여기서는 그게 맞는 답이다. 이름이 겹치면 접미사를 붙이고 **실제로
만든 이름을 돌려준다** — tmux 는 중복을 거부만 하고 골라 주지 않으므로, 그대로 두면
enter 가 그냥 실패하는 것으로 보인다.

### Flattened tree list model

There is no nested view. `ui/tree.go`'s `flattenMenu` produces a flat `[]listItem` — action rows, then each session, then (if expanded) its windows, then (if expanded) their panes — and `Model.cursor` is an index into that slice. `treeState` holds the expansion sets plus the window/pane caches; expansion survives session refreshes, and `pruneCaches` drops entries for sessions that vanished.

Call `rebuildItems()` after any change to sessions, filter text, ordering, or expansion state, or the cursor will point at a stale row. Action rows are only included in the unfiltered top-level view.

### Click selects, double click commits

Mouse reporting is on (`tea.WithMouseCellMotion()` in `cmd/mux/main.go`), which costs the terminal's own scroll and drag-select — Shift still gets them. A press maps to an item index through `chromeTop()` (title, plus the filter/error bar when there is one), the column's border, and `listOffset` — the *same* function `renderListView` scrolls by, so a row the user can see is a row they can hit. `chromeTop`, `panelHeight`, `listWidth` and `extraBar` exist for that reason: `viewMain` used to compute them inline, and a click map that recomputed them separately would drift the moment the chrome changed.

A single click only moves the cursor. Unlike the watch panel — where a click switches outright because there is nothing to read first — the right column here *is* the thing to read, so clicking to look and clicking to leave must not be the same gesture. The second press on the same row inside `doubleClickWindow` commits, and it commits through `activateCurrent()`, which `enter` also calls: a click that attached somewhere other than where the key would is a bug nobody would think to look for.

### 목록은 프로젝트로 좁혀서 열린다

tmux 세션은 **서버** 단위로 존재하고 서버는 사용자당 하나다 — 어느 VS Code 창에서
열든 `tmux list-sessions`는 전부 같은 목록을 준다. 좁히는 건 mux의 몫이다.

`NewModel`은 시작할 때 `tmux.ProjectScope()`를 한 번 호출해 `filterText`의 초기값을 정한다.

**좁힐지는 세션의 태그가 아니라 보고 있는 클라이언트가 정한다.** 처음에는 태그만 보고
좁혔는데, 그러면 Windows Terminal 에서 연 목록까지 같이 좁혀졌다 — 거기서 세션
매니저를 여는 것은 전체를 보려는 것이라 정반대다. 좁혀서 이득인 쪽은 이 폴더를
이미 연 편집기 창 하나뿐이다. 훅과 달리 여기서는 클라이언트를 물어봐도 된다:
사람이 방금 키를 눌러 뜬 화면이라 현재 클라이언트가 반드시 있다.

`#{client_pid}`와 `#{@project_dir}`을 한 포맷에 실어 **왕복 한 번**으로 끝낸다. 둘은
시작할 때 같이만 쓰이고, 두 번 부르면 Model 을 만드는 모든 테스트가 개발자의 실제
tmux 서버를 상대로 그 값을 두 번 치른다. `@project_dir`은 `tmux-project` VS Code
프로필이 세션을 만들 때 다는 태그이고, 태그가 없으면 시딩할 값이 없어 지금까지처럼
전부 보인다. **이걸 버그로 보고 지우지 말 것** — `esc`가 탈출구다.

별도의 스코프 모드를 두지 않은 이유는 필터가 이미 그 일을 하기 때문이다.
`applyFilter`는 이름·`Directory`·`ProjectDir` 셋을 대조한다. `ProjectDir`이 필요한
이유가 핵심이다: `Directory`는 활성 pane을 따라 움직이고 Claude의 cwd로 덮이기까지
하지만, `@project_dir`은 세션이 열린 폴더로 고정이라 pane이 딴 데 가 있어도 그 세션은
자기 프로젝트 목록에 남는다. `listFormat`이 `#{@project_dir}`을 싣고 오므로 tmux
호출은 늘지 않는다 — 대신 `parseLine`의 `SplitN`은 12이고(마지막 필드는 이제
`@mux_note`다), 필드를 더하면 그 숫자와 테스트 픽스처, 그리고
`scripts/tmux-assumptions.sh`의 필드 수 단언을 같이 고쳐야 한다. 하한은 11로 남아
있는데, 그 이유는 아래 "세션 메모는 tmux 옵션에 산다"에 있다.

시딩된 필터에서는 액션 행("새 셸"/"새 세션")을 계속 보여준다(`m.filterText == m.projectDir`).
사용자가 친 필터가 아니라 기본 시야이므로, 여기서 액션이 사라지면 VS Code에서 연
mux는 새 세션을 만들 길이 없다.

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

### 패널의 두 버튼: 왼쪽은 전환, 오른쪽은 에디터

패널에서 **왼쪽 클릭은 세션 전환**이고 **오른쪽 클릭은 그 세션의 프로젝트 폴더를
VS Code 로 여는 것**이다. 오른쪽 클릭은 **전환하지 않는다** — 지금 일하던 터미널을
떠나지 않고 다른 프로젝트의 에디터만 여는 것이 이 제스처가 있는 이유이고, 옮기고
싶으면 왼쪽 버튼이 바로 옆에 있다.

**옵션이 없는 것이 결정이다.** 처음에는 `@mux_panel_editor` 뒤에 두고 모든 클릭이
에디터를 열게 했는데, 옵션이 막으려던 것은 "훑어보려고 누른 클릭에 창이 뜨는 것"
하나였다. 버튼으로 가르면 그 위험 자체가 없어진다 — 오른쪽 버튼은 실수로 눌리지
않는다. 그래서 제스처가 opt-in 이고, 옵션은 지웠다. 되돌리고 싶어지면 그게 무엇을
막아 주는지부터 물을 것.

**tmux 설정에 더할 것이 없다는 것은 실측이다.** tmux 기본 `MouseDown3Pane` 은
`if-shell "#{||:#{mouse_any_flag},…}" { select-pane -t = ; send-keys -M }
{ display-menu … }` 이고, 패널 pane 의 `mouse_any_flag` 는 1이다(Bubble Tea 가 마우스
리포팅을 켜므로). 그래서 참 분기를 타 오른쪽 클릭이 그대로 앱에 전달된다. 거짓
분기였다면 tmux 자신의 pane 메뉴가 떴을 것이고, 이 기능은 사용자 tmux.conf 를 고쳐야만
동작했을 것이다.

그 바인딩이 `send-keys -M` **앞에 `select-pane -t =` 를 돌린다**는 것이 두 번째로
중요하다: 오른쪽 클릭 뒤 패널은 active pane 이 되어 있다. 그래서
`openSessionEditor` 도 `restoreFocus()` 를 부른다. 전환과 달리 **뒤이어 사용자를
패널에서 꺼내 줄 것이 아무것도 없으므로**, 여기서 빠뜨리면 포커스가 패널에 남는다.

**키보드는 뜻이 하나다.** `enter` 와 `mux nav enter`(= `prefix + Enter`)는 왼쪽 클릭과
같이 전환만 한다. 오른쪽 버튼에 해당하는 키는 없다 — 필요해지면 `navKeys`·`navBinds`·
`handleKey`·`cmd/mux` 를 다 고치고 사용자가 `mux setup-panel` 을 다시 돌려야 하므로,
그 값을 치를 이유가 생기기 전까지는 두지 않는다.

**`editorBin` 하나가 유일한 스위치다.** `RunWatch` 가 시작할 때 `findEditor()` 로 한 번
채우고, 비어 있으면 오른쪽 클릭이 no-op 이다. 제로값이 off 이므로 `watchModel{...}` 을
손으로 만드는 기존 테스트들이 아무것도 실행하지 않는다 — `exePath` 가 자가 재시작을
끄는 것과 같은 방식이고, 이게 `go test ./ui` 를 개발자의 데스크톱에서 떼어 놓는다.

**폴더는 `ProjectDir` 우선, 없으면 `Directory`.** `types.go` 가 이미 이유를 적어 뒀다 —
`Directory` 는 활성 pane 을 따라 움직이고 AI 의 cwd 로 덮이기까지 하지만 `@project_dir`
은 세션이 열린 폴더로 고정이다. `listFormat` 이 둘 다 실어 오므로 tmux 호출은 늘지
않는다.

**PATH 만 보면 안 된다** (`findEditor`, `ui/editor.go`). WSL 에서는 Windows 설치본이
interop 으로 tmux 서버의 PATH 에 올라와 있어 `LookPath` 로 끝나지만(실측:
`/mnt/c/.../Microsoft VS Code/bin`), snap·flatpak·`/opt` 타르볼·macOS 앱 번들은 `code`
라는 이름을 PATH 에 하나도 남기지 않을 수 있다. 그때 "설치 안 됨" 이라고 답하는 것은
사용자가 보고 있는 애플리케이션에 대해 틀린 말이다.

**`Start()` 가 아니라 타임아웃 걸린 `Run()` 인 이유가 있다.** 패널은 tmux 서버에서
환경을 물려받고 거기엔 `WSL_INTEROP=/run/WSL/<n>_interop` 이 들어 있다(실측). 그 소켓은
WSL 세션과 함께 죽는데 tmux 서버는 그보다 오래 살므로, 그 뒤로 `/mnt/c/...` 실행은 전부
실패한다. 띄우고 잊으면 그 실패는 아무 데도 닿지 않고 **클릭이 아무 일도 안 하는 것처럼**
보인다. 종료 코드를 기다려야 `⚠ VS Code 실행 실패` 가 패널 로그에 남는다. 이미
`tea.Cmd` 의 고루틴 안이므로 기다리는 대가는 이 커맨드의 메시지가 늦는 것뿐이다.
타임아웃(10초)은 멈춘 셤이 고루틴을 영영 붙잡는 것에 대한 상한이지 정상 동작의 일부가
아니다.

`Setsid` 는 패널 pane 이 닫힐 때 반쯤 뜬 에디터가 같이 죽지 않게 한다 — tmux 는 pane 의
프로세스 그룹에 신호를 보내고, 셤이 진짜 애플리케이션에 넘기기 전까지는 아직 우리
그룹에 있다. stdin/stdout 은 `nil`(=`/dev/null`)이어야 한다: 패널은 alt-screen TUI 라
자식이 한 줄만 써도 목록 위에 남는다. stderr 만 받아 `firstLine` 으로 한 줄로 줄여
에러에 붙인다(`tmux/runner.go` 의 `withStderr` 와 같은 이유, 폭이 40칸이라는 것이 추가
이유).

**실패는 `m.local` 에만 넣는다.** 이 pane 의 실행이 실패한 것이지 다른 창의 소식이
아니다 — `switchFailedMsg` 가 공유 로그에 가지 않는 것과 같은 규칙이다.

### The line above the pane you work in

`pane-border-status top` plus a `pane-border-format` that calls `mux border` (`ui/border.go`, `cmd/mux`'s `runBorder`, installed by `SetupPanel`) puts the session's summary — name, directory, tool, live state, branch — on the *other* column, above the shell. It is the only row of that side mux gets to write in: the pane is the user's terminal, and its title already belongs to whatever runs there (Claude Code names its pane after the task in hand, so taking it over would cost more than the line gives).

`BorderLine` breaks three of this codebase's own rules on purpose. It emits **no ANSI**, because tmux styles formats with `#[fg=…]` and paints raw escapes as text — colour is left to `pane-border-style`, and the state glyph carries what a colour would have said. It **does not right-align**, because tmux fills what the format leaves unused with `─`, so padding to the full width would replace that run with blanks and break the border. And it **gives up its parts from the right** — branch, then the tool's name, then the directory — never the session name, since a border that cannot say which session it belongs to has nothing worth drawing.

Two things were measured rather than assumed. `#()` **does** re-run inside `pane-border-format` (verified on an isolated server: renaming a session changed the line without a restart), on the `status-interval` cadence — the manual only documents this for the status line. And the border **costs one row per pane**, the panel's included: measured 20 → 19 for one pane, 20 → 18 with a second work pane.

The format's conditional skips the panel's own pane by matching `panelCommand` against `pane_start_command`. Deciding it in tmux rather than in `mux border` means the panel spawns no process at all, and `mux border` never has to work out which pane it is being asked about. **The line carries two levels of quoting for two parsers**: double quotes for tmux (which is what stops `#` in `#{pane_id}` starting a comment) and single quotes around the path inside `#()`, which `/bin/sh` reads — `TestPanelBlockQuotingKeepsPaneIDInsideQuotes` pins both.

`mux border` is a fresh process per pane per refresh, which is why it uses `tmux.SessionForTarget` rather than `ListSessions`: one `display-message` with list-sessions' own format, handed to the same `parseLine`. It prints an empty line and exits 0 on any failure — an error string here is not reported anywhere a user can act on it, it is simply painted across the top of the pane they are working in and stays there.

**The panel has two keyboards, and that is deliberate.** `prefix + Tab` (`FocusPanel`, `tmux/panel.go`) steps *into* the pane and back out; while you are in it, `watchModel.handleKey`'s own `↑↓ j k g G enter` apply directly, and `esc` leaves without choosing. Stepping back out is `select-pane -l`, the same mechanism `restoreFocus` uses after a click, so it returns to the pane you came from however many sit beside the panel. A window with no panel does nothing and exits 0 — the binding is global, and opening one is `prefix + a`'s job.

The second keyboard is the one that does *not* move the focus, and it remains the default way to use the panel:

**Keys reach the panel without focus reaching it.** `mux nav <up|down|top|bottom|enter>` resolves the window's panel pane and `send-keys` to it. The directions are a vocabulary, not raw key names, so `handleKey` can change without every user's tmux.conf changing. A window with no panel exits 0 — the binding is global and a failing `run-shell` writes to the status line on every press. `mux setup-panel` installs these as `navBinds`: `M-Up`/`M-Down` with no prefix, so steering costs one keystroke, and Alt rather than the arrows alone so they cannot collide with tmux's own `prefix + ↑↓` pane navigation. **Committing is the exception and is bound behind the prefix** (`prefix + Enter`). It was `M-Enter`, and a rootless binding takes that key from every program in every pane — Alt+Enter is how Claude Code inserts a newline, so mux was breaking the tool it exists to sit beside. `TestPanelBlockLeavesAltEnterAlone` pins it.

`q` still *quits* `mux watch`, closing the pane. Now that the panel can hold the focus that is a key someone may press by habit, so `esc` is the one documented as leaving; a panel closed that way comes back on the next hook or on `prefix + a`.

The panel speaks Korean and the TUI speaks English. `aiStateLabel` (`ui/notify.go`) is the panel's side of that and `AIState.String()` is the TUI's. Do not mix them in one pane. `aiWaitingLabel` is the same rule for *what* a blocked session is waiting on — but only for Claude's two fixed phrases (`input needed`, `permission prompt`); the field otherwise carries the command Claude wants to run (`Bash: git push`), and a command is not prose to translate, so anything unrecognised passes through verbatim. `ui/status.go` leaves it raw on purpose: that surface already prints `AIState.String()`, so it is the English one.

**The detector remembers a timestamp, not just a state** (`aiSnapshot`). Claude stamps every status change, one blocked prompt replacing another included, and with only the state to compare those look exactly like no change — the panel went silent on the transition it exists to report. `newPrompt` is the extra rule, and it needs *both* sides stamped: everything screen detection finds arrives with `AISince` zero, and treating that as "changed" would make every tick of a blocked session an event.

**A session that goes away gets one line** (`○ 종료`), then is forgotten. It is the one transition with no state left to report — the badge that would have said so went with it, and without the line the row just stops being drawn, which reads the same as a session going quiet. Only sessions that held a live state qualify; closing a plain shell is not news. The glyph is `○` because `TestGlyphWidthsAreStable` already pins it at one cell, and a state glyph that measures wrong shifts every column after it.

Two action rows — `셸로 나가기` and `새 세션` — were added above `🔔 AI 세션` and then removed at the user's request. Worth knowing if they come up again: the first attempt drove them through tmux's `command-prompt`, which failed in the field with a status-line error mux could not report (everything after the prompt appeared happened outside this process). The second ran `mux new` in a popup, which worked. The rows themselves went because the panel is read at a glance and two rows of chrome at the top cost more than they returned.

`mux watch` is a pane and not a popup for a reason worth not re-litigating: tmux has no floating window that leaves the keyboard alone. `display-popup` is documented as "Panes are not updated while a popup is present" — it freezes what is behind it and takes input. A pane is the only always-visible tmux surface that still lets you type, which is also why the companion `my-mux` widget is a GTK3 dock window on the desktop rather than anything inside a terminal.

It runs as a **separate process**, so it shares no state with the TUI: its own `prevAIStates` — the codebase's only cross-tick history, since a transition can only be seen by diffing — its own TTL caches, and a slower 2s tick (the 500ms rate exists for the TUI cursor row's preview, which reacts to a keystroke; this one reacts to a click).

**The panel is three parts, in the order they earn their rows**: a row per session, one line under each saying what last happened there (`sessionEventLines`), and the chronological log of everything below that (`notifyEventLines`, `ui/notify.go`).

The middle part is the one that was missing. A badge says what a session is doing *now*; it does not say what it just finished, and reading that off a flat log meant scanning past whichever session was busiest — measured on a live server, forty rows of one session repeating `⏳ 작업 중` / `✅ 작업 완료`. A version that opened the full history under the cursor alone was tried in between and removed: it left every *other* session as a badge and nothing else, and the panel used sixteen of sixty-seven rows.

Those lines carry **their session's name**, and sit **directly under its row**. `sessionAtRow` and `sessionOrder` index the same slice, so that one field makes the line click to the same place as the row above it, and `sessionOrder` — which folds *consecutive* rows with the same owner — gains no extra cursor stop.

The per-session line is **all-or-nothing**: bought only when every session can have one, because a list where some rows carry a line and others do not reads as missing data rather than as a short pane. That is why `notifyLines` renders the session block twice — deciding it means knowing how tall the list is without it, and computing that arithmetically would put the block's layout rules in two places. Rendering costs string assembly, and `sessionLines()` already redraws everything on every click.

**A blank sits between sessions, and it was taken out once.** The reasoning was that it did two jobs — air, and a second row so a click landed on a block rather than a single line — and that the per-session line had taken over the second. What that missed is that the first job was the load-bearing one: seven blocks of two lines each run together into a wall, and a panel whose whole point is being read at a glance stopped being readable. The blank carries **its session's name** like every other row in the block, so the click target stays whole, and it is emitted only between sessions (`i < len(ss)-1`) so the list does not end on a gap. Groups are still separated — a rule and blanks around the second group, a break before the log — and `TestNotifyBlocksAreClickable` pins that no row *inside* the list belongs to nobody.

The blank follows the highlight only when nothing else sits under the row (`sel && len(last) == 0`): with an event line present the highlight ends at the session row, and inverting a trailing blank as well would draw a two-row bar for a one-row cursor.

The log takes what the sessions left and is **the thing that yields**: it is last, so `fixedBox` clipping it costs least, and below two rows it draws nothing rather than a heading over an empty section.

**A logged transition names the work, and falls back to timing it.** The glyph already carries the state, so `transitionText` (`ui/notify.go`) spends the words on what it cannot: Claude's own name for the turn (`⏳ panel-session-last-notification`). That name comes from the state file's `name`, but only when `nameSource` says the tool chose it — `auto` or `derived`, or missing, which 12 of 67 files here are. A name the *user* set is dropped by `claudeTaskName` (`tmux/claude_status.go`), because it does not move: two hand-renamed sessions carried the same label for a day while Claude renamed one of its own three times in an afternoon, and a label repeated on every transition is exactly what this field exists to stop.

Where there is no name, a *finished* turn says how long it took instead (`✅ 작업 완료 · 3m 12s`). `turnLength` needs **both** ends stamped: screen detection carries no timestamp, so a zero `since` read as an epoch would report a turn as having taken fifty-six years. A turn that just started has nothing to measure and keeps the plain label.

The per-session line is drawn **unhighlighted** while the session row and its reason line are not. What the highlight is for is saying precisely which session the cursor is on, and a second inverted line under every selected row blurs that.

**No new gesture, deliberately.** `M-Up`/`M-Down` already move the cursor, and click and `enter` already mean "switch to this session" — a new `mux nav` verb would need `navKeys`, `navBinds`, `handleKey` and `cmd/mux` changed, and would stay dead for anyone who does not re-run `mux setup-panel`.

**A logged transition names the work, not the state** (`transitionText`, `ui/notify.go`). The glyph already carries the state, so the words are free for what it cannot say: Claude's own name for the turn in hand, read from the `name` field of its state file (`claudeSessionFile.Name` → `ClaudeStatus.Name` → `Session.AITask`). `작업 중` and `작업 완료` were the same two sentences on every row of the log — true, and nothing anyone could act on. Every live session file observed carried a name.

The label is the fallback, not the format: a tool that publishes no task still has a state worth reporting, and a bare glyph is not a sentence. Approval keeps `승인 대기 · <이유>` — what it is blocked *on* outranks what the turn is called.

The task name is **not** on the session row, and that was measured rather than assumed: at the panel's 47 columns only one of five real sessions could seat both it and the branch, so putting it there costs the branch on most rows. The line under each session has the whole width to itself and sits inside the same block, which is where it goes.

**The state *now* comes from `m.sessions`; the log says what happened.** The row's badge and age are the complete answer for the former and the log is not — but the line under it adds three things the row cannot: a wall clock, the transition before the current one when the log has moved past it, and the reason a session was blocked on after it stops being blocked (the reason row draws only while `AIStateApproval` holds).

**`trimLog` (`tmux/eventlog.go`) is what makes a line per session possible.** A plain head-slice is recency-only, and recency starves the quiet: measured, fifty entries covering sixty-four minutes, twenty of them one session's, and two of seven running sessions with nothing in the log at all. So the cut keeps the top 50 *and then* the newest entry of every session those 50 missed. The value stays a `[]PanelEvent` and stays sorted — the tail it draws from is newest-first too, so everything appended is older than the last one kept — which is why `sortEvents`, `duplicateAt` and the lock-free merge are untouched.

Raising `MaxPanelEvents` instead was measured and rejected: on tmux 3.4 a `set-option` argv of 16000 bytes goes through and 20000 comes back `command too long` (`MAX_IMSGSIZE`), so at ~90 bytes an entry the ceiling is near 170 — and the order would still be recency, so a session idle since morning falls off anyway.

**Sessions that no longer exist are dropped from the log** (`MergeEvents` takes the live names, from `sessionNames` in `ui/watchevents.go`). Nothing draws them, and without this the keep-one rule would hold a dead session's last event forever, since nothing newer ever arrives to replace it. Measured: six of fifty entries belonged to a session that had already gone; pruning them took the log to 44.

**The event log is the one thing panels do share**, and it lives in the tmux server: the global option `@mux_events` (`tmux/eventlog.go`), read and rewritten by every panel on each tick. Detection stays per-process — only a diff can see a transition — but what it observes goes somewhere all of them read. Without that, a panel created a minute ago opens on `아직 없음` beside panels showing hours of history, because `detectTransitions` records a first sighting rather than reporting it, and every session is a first sighting to a new process.

There is no lock and none is needed. Every panel watches the same server-wide session list, so one transition is observed by all of them and written by each; the redundancy *is* the retry, and a write that loses the race drops an entry another panel is about to write again. What that demands instead is that the same transition produce the same entry everywhere, which is `duplicateAt`'s job: `(session, state, AISince)` when Claude timestamped the state itself, and "same session, same state, within ten seconds" for everything screen detection finds — `ScreenState` carries no timestamp, so every tool but Claude arrives with `AISince` zero.

Two things about the panel side are easy to undo by accident. The merge is a **`tea.Cmd`**, not work done inside `Update`: `ui` has no mock runner and several tests call `Update(sessionsLoadedMsg{…})` directly, so merging inline would make `go test ./ui` write options into the developer's own tmux server. And `switchFailedMsg`'s `⚠ 전환 실패` stays in `watchModel.local` — it says *this pane's* click failed, which is not news in anyone else's window. `combineEvents` interleaves the two by time at render.

**The option dies with the tmux server, and a file beside `panel.json` now outlives it** (`tmux/eventarchive.go`). This used to say the option was the whole answer, and the argument had a premise hiding in it: *the server's lifetime ≈ the user's*. On 2026-09-01 that premise broke — `/tmp` was emptied, `/tmp/tmux-1000/default` was unlinked, and a server with seven sessions and thirty live Claude processes went on running while nothing could reach it. A fresh server took the path and the whole log went with it.

So the two now have different jobs. The option is the **drawn** view: server lifetime, pruned to live sessions by `trimLog`, read by every panel every tick. `events.json` is the **archive**: machine lifetime, never pruned by liveness, read only when there is something to restore. The old "nothing has to clean it up" still holds — `trimArchive` drops anything older than fourteen days.

**Restoring is per-session, not a one-shot seed.** "If the option is empty, load the file" fires exactly once, at the worst moment: right after a server dies the option is empty but only one or two sessions have come back, so the seed is immediately pruned to those and a session that returns three minutes later never gets its history. The rule is instead: on every merge, any *live* session with no entry in the option gets its **most recent** archived entry back — one, not all of them. That subsumes the empty-option case (on a fresh server every session is missing), is self-limiting (once a session has an entry it is a no-op), and cannot resurrect the dead, since it is keyed to the same `live` list `trimLog` prunes on.

**The backfill must run before `trimLog`, and the archive must union the *pre-trim* slice.** Both are load-bearing and both were got wrong first. Backfilling after the trim lets the option grow past the 16KB `MAX_IMSGSIZE` wall, and that `set-option` failure is discarded — the log would stop updating silently. And unioning the post-trim slice means a session dropped by `trimLog` in the same merge never reaches the file at all, which leaves the archive able to keep only what it already had. `TestArchiveKeepsWhatTrimLogDropped` is the test that encodes the whole design; deleting it reverts the feature to the bug.

**One entry per session, and that number was measured.** Splicing a missing session's whole history was the first version. On 2026-09-07 WSL rebooted and exactly one session came back, so that session's entire archive went into the option and `trimLog`'s fifty slots filled with pre-reboot entries — the panel's "최근 이벤트" showed six-hour-old work on a one-minute-old server. One is the right number because one is what the panel draws per session; the option is the record of what has happened on *this* server, and a dead server's history must not compete for its fifty slots. Anything older is still in the archive.

The file is written on its own comparison, not the option's. The two early returns in `MergeEvents` ask whether the *option* changed, so a mirror hung off them never writes on a steady-state tick nor on a fresh panel's first merge — the file would be stale exactly when the server dies. It also loads lazily: with nothing to backfill and no option write, an idle tick opens no file at all, which is what keeps seven panels off the disk every two seconds. There is no lock, for the same reason the option has none, and the union is what makes that safe: a writer that loses the rename race has its entries restored on the next tick from the option, which is the converged shared copy.

**A tmux server that lost only its socket is revived, not mourned** (`tmux/revive.go`). `man tmux`: *"If the socket is accidentally removed, the SIGUSR1 signal may be sent to the tmux server process to recreate it (note that this will fail if any parent directories are missing)."* That is the whole fix for the incident above, and it existed the entire time — it just required a human who knew about it. `reviveAndRemember` in `cmd/mux` runs it at the TUI and panel entry points; `runPanelAuto` revives but does not record, because it is the hook path and must not grow a round trip.

**The server is identified from a note, never from a scan.** `RememberServer` asks `display-message -p '#{pid} #{socket_path}'` — one round trip, because the two are only ever used together — and writes them to `~/.config/mux/server.json` with the PID's start time. Scanning `/proc` for something that looks like a tmux server was the obvious alternative and is the dangerous one: the heuristics that separate a server from a client (no tty, PPID 1) fall apart on macOS, and when two servers are alive — which is exactly what this incident produces — reviving the wrong one orphans the server the user is actually in. That is not a bug you can undo.

`procStart` is stored for the same reason and is the load-bearing guard: `processAlive` (`tmux/proc.go`) compares it against `/proc/<pid>/stat` field 22, so a dead server's recycled PID cannot be signalled. Where it could not be recorded — no `/proc`, i.e. macOS — the feature stands down entirely rather than signal on existence alone. `/proc/<pid>/comm` reading exactly `tmux: server` is the second check, because a signal cannot be taken back.

**It never acts while the socket exists.** A socket at the path means someone is serving it — either our server is fine, or a *new* server has taken the place. Signalling in that second case makes the old server seize the path back and orphans the one the user is now using, which is the accident this code exists to prevent, caused by the code meant to prevent it. Recovery is also attempted once per process: firing at a dead PID every two-second tick is noise, not recovery.

`scripts/tmux-assumptions.sh` section 10 measures all of it against a real isolated server — that `#{pid}` names a live process whose `comm` is `tmux: server`, that `#{socket_path}` is the real path, that removing the socket makes calls fail while the server keeps running, and that SIGUSR1 brings back both the socket and the sessions. 29/29 on tmux 3.4.

**`ui/remote.go`'s socket moved out of `/tmp` for the same reason** — `~/.config/mux/tui.sock`. Its old comment justified `os.TempDir()` as "a fixed path that does not depend on environment variables", which was simply false: `os.TempDir()` reads `$TMPDIR`. `os.UserConfigDir()` is the more deterministic of the two and matches how `preferences.json` and `panel.json` resolve. When `/tmp` was emptied the TUI stayed up while its socket vanished, and `mux switch` failed with nothing to say.

**The panel picks up a new binary by exec'ing into it.** `mux watch` is one process per pane and lives for days, so installing mux left every open panel running the code it loaded at startup — `/proc/<pid>/exe` reading `(deleted)` was the only sign, and the symptom was a fix that visibly did not take. There is no window event after an install, so `panelHooks` cannot help: nothing fires.

`watchModel` carries the path it started from and a stamp of that file (`ui/restart.go`), re-stats it each tick, and on a change quits so `RunWatch` can `syscall.Exec`. The pane, its width and its focus all survive, because only the process in it changes — measured: the PID stayed put and the exe inode moved from the unlinked one to the new one 4.5 seconds after an install.

Three things about it are load-bearing. **A changed reading has to arrive twice, and the two have to agree with each other.** `install` cannot write over a running binary — measured, that is `ETXTBSY`, which is why `cp` fails there and `install` unlinks first — so the path names a partially written file while the copy runs, and one disagreeing reading arrives long before there is a binary to exec into. **A failed exec must not return**, because `RunWatch` returning ends the process and takes the pane with it; the loop puts the panel back up on the code it has, and the fresh baseline it takes is of the file that would not run, which is what stops it retrying forever. And it **stands down while the pane is active**: exec keeps the focus but not `m.selected`, so restarting under someone steering with `prefix + Tab` and `j`/`k` moves their cursor mid-gesture.

Size and mtime rather than a hash, because this runs every tick and the answer is almost always no; the cost is that reinstalling a byte-identical build still restarts, which costs the cursor and nothing else. An empty `exePath` disables the whole thing — that is what every test gets, and it is what keeps `go test ./ui` from stat'ing anything or reaching the `PaneActive` call.

The TUI does not do this. It is opened, used and quit; the panel is the one that is still running a week later.

Every tmux call it makes must name its own pane (`selfPane()`, from `$TMUX_PANE`). tmux resolves an omitted target to the window's *active* pane, which is the one the user works in — the panel is created detached and never becomes active on its own. A width correction sent without a target shrank the wrong pane, and the panel grew to fill what it gave up.

**The panel names its own pane, and that title is the only thing that survives tmux-resurrect.** `MarkPanelPane` (`tmux/panel.go`) sets `panelTitle` from `RunWatch`, and `findGhostPane` closes a pane that still carries it but is no longer running the panel.

That indirection is forced. tmux-resurrect cannot restore `mux watch` itself: its save strategy records a pane's *child* process, and the panel is the pane process with no child, so the command it saves is empty and `@resurrect-processes` never matches — verified against the plugin, not assumed. What comes back is a shell in the panel's place, at the panel's width, whose `pane_start_command` now reads `cat <contents>; exec $SHELL`. Restore saves no user options at all, so `@mux_panel_*` cannot carry the mark either; it does restore the title verbatim, with no option to turn that off. Without the mark, `findPanelPane` sees no panel and the hooks open a second one beside the dead one — a three-way split on every server restart.

Three things about the cleanup are deliberate. It is a **second** `list-panes` rather than another field on the first, because the hook path almost always finds a live panel and must stay at one call — `TestTogglePanelDoesNotLookForAGhostWhenThePanelIsThere` pins that, and changing `findPanelPane`'s format string would also break twenty mock keys. The title is compared **whole**, since this closes a pane and the only thing worse than a leftover one is closing the pane someone was in. And it runs **before** the width, VS Code and manual-off checks: a window mux refuses to put a panel in is the window that wants those columns back most.

**A ghost that is the window's only pane is untitled, not killed.** An exact title is not proof either: resurrect restores titles verbatim, so a user who kept working in the restored shell owns a pane named exactly `mux panel` — and when the window holds nothing else, `kill-pane` is `kill-session`. Measured 2026-08-30: two sessions lost that way, via nothing more exotic than closing the panel and reopening it. `findGhostPane` counts the panes off the same `list-panes` output (no extra call), and the alone case clears the title with `select-pane -T ""` — the pane is never mistaken for a ghost again, and the split that follows puts the real panel beside it. With another pane in the window the ghost is still killed; that is the case the cleanup exists for, and there it does not take the session with it.

Marking happens in `RunWatch` rather than at split time so a panel someone started by hand with `mux watch` is marked too — the same reason `panelCommand` matches that pane. A panel created before this existed has no title and is not recognised; it heals the first time one is opened and saved.

**The panel is one width, everywhere.** It opens at `SavedPanelWidth()` from `~/.config/mux/panel.json`, and at `defaultPanelWidth` (36) only when there is none. `rememberPanelWidth` writes that file on every drag, and **every open panel follows it on its next tick** (`adoptSavedWidth`, `ui/watch.go`) — someone dragging a border is saying how wide the panel is, not how wide it is *here*.

There used to be a third source ahead of the file: a per-session `@mux_panel_width` tmux option, which won. That is gone. It meant a drag in one session left every other panel where it was, which is not what one mouse and one number feel like they should do. The disk copy is deliberately one number and not a per-session map for the same reason it always was: session names are exactly what a server restart does not carry over. It is deliberately not `preferences.json` either — the TUI reads that into memory at startup and writes the whole struct back, so a width saved by the separate `mux watch` process would be clobbered by the next sort toggle.

**Adoption clamps but never writes back.** A panel in a narrow window takes `MaxPanelWidth` instead of the saved value, and must not save that — or one cramped window would drag every other panel down to its size. The guard is in `applyResizeWith`: a resize landing on the width we already wanted is not a drag and remembers nothing. That same guard is why an adopt-triggered resize does not loop back through `rememberPanelWidth`.

That was the rule and the first-size branch broke it: it clamped *and* called `rememberPanelWidth`, so a panel opening in a 149-column window put its 74 in the shared file and every other panel adopted 74 within two ticks. Observed by accident — a throwaway 172-column window took the saved width from 52 to 43 and five live panels followed it down. The branch now corrects the pane and writes nothing. `TestFirstSizeClampDoesNotWriteBack` is what keeps it that way; `TestAdoptSavedWidthClampsWithoutWritingBack` only ever covered the other path.

**The ceiling is applied before anyone can see it, too.** `MaxPanelWidth` lives in `tmux/panelwidth.go` beside `MinPanelWidth` and for the same reason: both ends of the round trip have to agree on one number. `TogglePanel` clamps the width it opens a panel at, and the panel clamps what it will hold — unclamped creation meant a panel opening at the saved 100 in a 149-column window and shrinking to 74 on its first frame, a guaranteed flash on every window it appeared in. The window width there is read lazily and cached, so the auto gate's cheaper checks still short-circuit ahead of it and the hook path costs nothing extra.

**The tick must not measure the ceiling against a stale window.** `m.winWidth` is whatever the last resize *event* reported, and four paths leave it behind: a window that changes size without changing this pane's (no `WindowSizeMsg` at all), a mid-relayout reading of zero, a narrow reading, and a border drag. Clamping against a stale number turns one re-layout into two visible corrections exactly `watchInterval` apart. `adoptSavedWidth` re-reads the window — but only when the ceiling would actually bite, so the ordinary tick still costs no tmux call — and hands the decision to `adoptSavedWidthWith`, which is pure. That split is not cosmetic: `ui` has no mock runner, so a test calling the impure one reaches the developer's own tmux server and gets a different answer from it. One did.

`MinPanelWidth` (`tmux/panelwidth.go`) is the floor on both ends of the round trip, which is why `ui`'s `notifyMinWidth` is an alias of it rather than its own 24: a hand-edited `panel.json` must not open a pane the renderer immediately gives up on. `defaultPanelWidth` is the narrowest the panel still answers its questions at — below about 32 the branch column goes and event text gets cut — not the narrowest it survives, which is much lower because a user dragging deliberately gets to choose.

**A pane appearing or dying beside the panel is not a drag, and the width alone cannot tell.** `applyResize` reads `#{window_width}` and `#{window_panes}` in one call (`tmux.WindowShape`) — one call because a width read now against a count read a tick ago would misread a split as a drag, which is the thing the count is there to prevent. A changed count means tmux re-laid the window out; only an unchanged one can be a border drag.

The ceiling below was supposed to cover this and could not. tmux hands a closing pane's columns to a neighbour, and in a two-pane window that lands on *exactly* half — which is not **over** half, so it passed, was saved to `panel.json` and then actively held. Observed in the field at 118 of 237 columns. Tightening the comparison to `>=` is not the fix: the correction target is itself `maxPanelWidth`, so the panel would restore to a width it immediately rejects, forever.

Pane width is held against tmux, not just recorded. With `aggressive-resize`, switching sessions resizes windows and tmux redistributes panes, so the panel drifted every switch. `applyResize` separates the two causes by reading the *window* width: unchanged window plus changed pane is a border drag (adopt it), changed window is a re-layout (undo it). Three rules keep it from running away. The window width is read **synchronously**, because getting it via a `tea.Cmd` let a stale value read as "window unchanged" and a momentarily squeezed pane became the enforced width. A width below `notifyMinWidth` is never adopted as intent. And a width above `maxPanelWidth` — half the window — is not either, because "same window, different pane" is *also* what a pane appearing or dying beside the panel looks like; measured, the panel reached 188 of 237 columns and held there, leaving 48 to work in. The ceiling both puts the pane back and caps the first size seen, so a width remembered from a roomier window heals instead of persisting.

**What is left is tmux's, and trying to beat it did not work.** When two clients of different sizes are attached, `window-size latest` (tmux's default) resizes a window to whichever client last used it, so switching a wide client into a window a narrow one was holding grows that window and tmux hands the extra columns out among its panes. Measured on an isolated server with a 172 and a 269 column client: the panel went 52 → 100 → 52, the wrong width holding for 20–45ms. The panel can only react after tmux has already re-laid out, so a correction after the fact is all there is.

Batching the correction into the switch — `switch-client … ; resize-pane …` in one tmux command list, on the theory that tmux redraws once at the end — was built and **removed**. Five trials each way were indistinguishable (plain 0/22/38/0/41ms against batched 0/33/0/44/21ms); tmux's command queue yields between items, so the intermediate layout is reachable either way. It cost two extra lookups per click and a fallback path for a benefit that could not be shown. If it is tried again, measure what the client *draws* rather than what `display-message` reports — polling observes tmux's state, not its output, and that distinction is what makes this hard to test.

The real cure is environmental: a stale narrow client pinning one window is what makes that window the only one whose size changes. Detaching it removes the wobble entirely.

**So the panel now removes it, on a timer** (`tmux/client.go`, `ui/watchghost.go`). The ghost is usually mux's own: `mux` typed outside tmux attaches and draws a popup (`AttachAndPopup`), and a terminal closed without quitting that popup leaves the attachment behind. Measured 2026-09-04 — a 269x69 client idle for seventeen hours held two windows at 269 while the live 237x53 client re-laid them out on every visit. That is the wobble `applyResize` had been correcting after the fact.

`StaleClients` is pure and takes `now`, and **all four of its rules are load-bearing** because the action is disconnecting somebody's terminal:

1. More than one client — a lone client is the user, however long they have been away.
2. Its size differs from the newest client's. **This is the harm**, and fixing exactly the symptom is the point: a same-size idler changes no layout, so taking it would be tidying at the user's expense.
3. Idle longer than the threshold.
4. Not the newest client — that one *is* the user, by definition of the only signal there is.

`@mux_ghost_client_idle` sets the threshold in minutes, read once in `RunWatch` like `MinWindowWidth`. **Zero is honoured here rather than falling back to the default**, unlike `MinWindowWidth`'s: this switch turns off something that disconnects a terminal, so "off" has to be reachable. Only a missing or unparseable value takes `DefaultGhostClientIdle` (30m).

Three things keep it from firing where it should not. It runs on **its own minute cadence**, not the 2s tick — one panel per window all asking the same server about a terminal abandoned yesterday. It is a **`tea.Cmd`**, for the reason `mergeEventsCmd` is: `ui` has no mock runner, so doing it inline would make `go test ./ui` disconnect the developer's own terminal. And `ghostIdle` **zero disables the whole check**, which is what every hand-built `watchModel` in the tests carries — the same mechanism `exePath` and `editorBin` use.

`DetachTTY` refuses an empty tty, and that guard is not cosmetic: `detach-client` with no `-t` disconnects whoever runs it, which from a panel is the user. `parseClientLine` drops a line it cannot fully parse for the mirror-image reason — a half-filled `Client` carries a zero `Activity`, which reads as epoch-old, precisely the shape that gets a client disconnected. The report goes to `m.local`, not the shared log: several panels see the same ghost and one wins the race, so sharing would draw a line in every window for what one pane did.

`scripts/tmux-assumptions.sh` §11 measures the whole chain against a real client (attached through `script(1)`, skipped where that is missing): the five-field format, that `#{client_activity}` is **unix seconds** — `#{t:client_activity}` renders a locale-dependent string that cannot be parsed — and that `detach-client -t <tty>` takes that client and leaves the session running. 40/40 on tmux 3.4.

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

### 세션 메모는 tmux 옵션에 산다

세션 행의 다른 모든 것은 mux 가 알아낸 것이다 — 이름, 디렉터리, 브랜치, AI 배지.
사람이 쓴 것은 `Session.Note` 하나뿐이고, 그게 `@mux_note` 세션 옵션이다
(`tmux/note.go`).

**옵션인 이유는 `@project_dir` 이 옵션인 이유와 같다.** `listFormat` 에 실으면
tmux 호출이 한 번도 늘지 않고 TUI·패널·`mux border` 가 같은 값을 본다. 그리고 옵션은
이름이 아니라 *세션* 에 붙으므로, `preferences.json` 의 `Orders` 가 rename/kill 마다
필요로 하는 보정 코드가 여기엔 없다. `sessionNoteMsg` 핸들러에 저장 코드가 없는 게
그 결과다 — 값은 이미 tmux 에 있고 다음 틱의 `loadSessions` 가 가져온다.

대가는 tmux 서버와 함께 죽는다는 것이다. 그 전제는 2026-09-01 에 한 번 깨졌고
(`tmux/eventarchive.go`), 그때는 메모도 같이 간다. 아카이브를 두지 않은 것은 결정이지
누락이 아니다 — 필요해지면 `events.json` 이 `@mux_events` 에 대해 하는 일을 그대로
복제하면 된다.

**`@mux_note` 는 `listFormat` 의 마지막 필드여야 하고, `parseLine` 의 하한은 11 로
남아야 한다.** 마지막인 이유: `SplitN` 의 잔여값 자리라 `|` 가 든 메모가 온전히 실린다
— 유일하게 사용자가 직접 치는 값이므로 그 성질이 필요한 것도 이 필드뿐이다. 하한이
11 인 이유: 한 칸 모자란 줄은 `parseLine` 이 거부하고, 거부된 세션은 목록에서 통째로
사라진다. 메모 하나 때문에 치를 값이 아니다. `scripts/tmux-assumptions.sh` 3절이
실측한다 — 쓰고, 포맷 마지막 칸으로 돌아오고, `-ut` 로 사라진다.

**`sanitizeNote` 가 개행을 지우는 것이 load-bearing 이다.** `list-sessions -F` 는 세션당
한 줄이므로, 개행이 든 메모는 그 세션의 줄을 둘로 쪼개고 양쪽 다 파싱에 실패해 세션이
목록에서 사라진다. 손으로 `set-option` 한 경우까지 막을 수는 없어서 읽는 쪽
(`parseLine`)에서도 한 번 더 통과시킨다.

**행에서는 브랜치가 먼저 양보한다** (`ui/list.go` 의 `formatSessionRow`). 지금까지
브랜치가 유일한 탄력 컬럼이었으므로 뒤집기 쉬운 순서인데, 근거가 있다: 브랜치는
프리뷰 헤더와 pane 위 border 가 다시 말해 주고, 메모는 사용자가 쓴 것이라 다른
어디에도 없다. `minNoteWidth` 아래로는 잘라 보이느니 통째로 버린다 — 글리프와
말줄임표만 남은 칸은 빈칸보다 나은 게 없다. `anyNoted` 가 `sessionNameWidth` 에서
자리를 예약하는 것은 `anyOrdered` 와 같은 이유다(스크롤 중에 이름 폭이 움직이면 안
된다). 그 예약은 **메모를 가진 세션이 하나라도 있을 때만** 지불되고, 대신 그런 목록의
이름 컬럼은 좁아진다.

**패널에서는 메모가 브랜치 옆이 아니라 세션 행 아래 줄이다.** 기본 폭 36칸에서
`notifySessionLine` 의 가용폭은 31칸이고 이름·나이·`◀`·브랜치가 이미 전부 쓴다 —
거기 메모를 끼우면 메모가 있는 모든 행에서 브랜치가 사라진다. 승인 대기 사유 줄이
쓰는 패턴을 그대로 쓴다: 같은 세션 이름을 달아 `sessionAtRow` 의 클릭 블록과
`sessionOrder` 의 커서 접힘을 유지하고, 이름과 같은 칸에서 시작하도록 두 칸 들여쓰고,
`width-1` 로 잘라 다른 행이 지키는 오른쪽 여백을 지킨다. 이벤트 줄과 달리
`perSession` 예산에 넣지 않는 이유는 "전부 아니면 전무" 규칙이 적용되지 않기
때문이다 — 메모는 원래 일부 세션에만 있다.

### 선택 모드는 틱도 멈춘다

`v` (`toggleSelectMode`, `ui/app.go`) 는 `tea.DisableMouse` 를 보내 마우스 리포팅을 끈다.
그것만으로는 절반이다: 목록이 0.5초마다 다시 그려지면 터미널이 선택 영역을 지우므로,
선택을 끝내기도 전에 사라진다. 그래서 `tickMsg` 핸들러가 선택 모드에서 **다음 틱을
예약하지 않고** 그냥 돌아간다 — 화면이 얼어야 긁을 수 있다.

되살리는 곳이 `toggleSelectMode` 한 군데뿐인 것이 그래서 중요하다. 선택 모드가 도는
동안 틱을 들고 있는 것이 아무것도 없으므로, 여기서 다시 걸지 않으면 목록이 영영 멈춘다.

`extraBar` 가 그리는 줄은 장식이 아니다. 설명 없이 멈춘 목록은 mux 가 죽은 것으로
읽히므로, 갱신이 멈췄다는 것과 어떻게 빠져나오는지를 같이 말한다. `chromeTop` 이
extraBar 유무로 마우스 행 보정을 이미 하므로 복귀 후 클릭 좌표는 저절로 맞는다.

무엇이 복사되는지는 어디서 도느냐에 달렸다. tmux 안이면 tmux 의 copy-mode, 팝업
안이면(팝업에는 copy-mode 가 없다) 터미널 자신의 선택 — WSL/Windows Terminal 에서
실제로 원하는 쪽이 후자다. 어느 쪽이든 **화면에 그려진 글자**를 긁는 것이라
`maxSessionNameDisplay` 나 `maxPathDisplay` 로 잘린 값이 복사될 수 있다.

패널(`mux watch`)에는 넣지 않았다. 별도 프로세스라 자기 토글과 자기 틱을 따로 가져야
한다.

### One AI badge, two facts

AI CLI detection is a single feature, not a generic layer with a Claude feature bolted beside it. `tmux/aitools.go` owns all of it: `aiToolMap` (which tools exist, their icon and color) *and* `AIState` (`None`/`Working`/`Approval`/`Ready`) with `AIState.Icon()` (`⏳`/`❗`/`✅`).

**The rule: a live state glyph replaces the tool icon, it never sits beside it.** `ui/status.go`'s `aiGlyph` is the one place that decides, and `ui/list.go`, `ui/preview.go`, and `mux status` all go through it. A separate state column would repeat what the tool icon already says, since only a detected AI CLI can have a state. Keep it that way when adding anything state-related.

The badge cell is padded to `badgeWidth` (2) even when empty, because state glyphs measure 2 and tool icons measure 1 — that padding is what keeps the git branch in one column across every row.

**The list is a table, and its columns are decided once per render.** `renderListView` computes `sessionNameWidth` and `anyOrdered` over the *whole* list rather than the visible slice, then hands both to every row: a column sized per row, or per screenful, would move the names as you scrolled. The name leads because it is what the eye looks for; the age, badge and branch follow it in fixed columns so the list is scanned down rather than read across, and the branch is flush right and is the first thing to yield when the row runs out of room.

Two markers became colours instead of columns, which is what freed the gutter from twelve cells to two. The attached session is its name in `colorAccent` (`nameColor`), the active window or pane likewise (`activeColor`) — a marker column costs every row a cell to say something about one of them. The `#3` order label is drawn only when some session in view carries an order.

The list's elapsed column shows the *state's* age for a session with live state (`sessionAge`) and the session's creation age otherwise, and **the recent sort keys off that same value** — `sortedSessions`' default branch, matching the panel's `sortByDisplayedAge`. It used tmux's `session_activity` and that is a different clock: measured on a live server, activity ran 40s → 53s → 1m → 52m in order while the column beside it read 39s, 1m, 28h, 5m, so a correctly sorted list looked unsorted. `session_activity` also says only that a pane drew something, which a session left running does constantly. The column takes the state color — with the glyph folded into the badge, that color is what still marks a blocked row at a glance.

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

**VS Code는 두 가지로 걸러진다. `@project_dir` 태그가 1순위다.** `SessionOnlyInVSCode`는 붙어 있는 클라이언트를 들여다봐야 답할 수 있는데, `after-new-session` 훅은 클라이언트가 붙기 *전에* 터진다 — 그래서 "VS Code 아님"으로 읽히고 패널이 들어갔으며, ensure 의미상 다시는 안 나왔다. `tmux-project` 프로필이 단 `@project_dir`은 세션이 생긴 순간부터 거기 있고 클라이언트와 무관하므로 그 구멍이 없다 (`SessionProjectDir`, `tmux/session.go`). 태그가 있으면 auto 경로는 물러난다. 태그가 **없는** 것은 증거가 아니므로 아무것도 바꾸지 않는다 — 그때는 클라이언트 검사와 폭 규칙이 그대로 판단한다.

`panelHooks` is the list of events after which a window can be on screen without a panel. Each was checked against a real server, since a hook that silently never fires looks exactly like a broken feature. `client-session-changed` is the load-bearing one: clicking a session in the panel runs `switch-client`, landing you in a window that never had a panel. There is no recursion — the ensure runs `split-window`, which makes a pane rather than a window.

`install.sh` sets up the popup keybinding only; `mux setup-panel` is the sole path for the panel block.

**The TUI offers the whole setup itself on first run** (`ui/setupoffer.go`, `tmux/setup.go`). `go install` delivers the binary but not the tmux side, and the setup commands are exactly what a new user cannot know about — so a bare `mux` whose config carries no mux region (`IntegrationInstalled`, checked in `runRoot` via `OfferSetupIfNeeded`) opens on a y/n page instead of the list. Detection is automatic, writing is not: only `y` touches the config. `InstallIntegration` reuses the same write helpers the setup commands do — not the commands themselves, which narrate to stdout under a TUI that owns the screen — and sources both regions into the running server in one call, so the keys work the moment `y` is pressed. The answer is recorded in `preferences.json` (`SetupOffered`) whichever way it goes: asked once, ever, and a decline leaves `mux setup`, the CLI bundle of both setup commands. The install runs as a `tea.Cmd` for the same reason the panel's event-log merge does — `ui` tests drive `Update` directly, and an inline install would write into the developer's own tmux config. The page itself follows `modeHelp`'s shape (enum value, no sub-model), and `mux new` never offers: someone mid-gesture is not someone to interrupt.
