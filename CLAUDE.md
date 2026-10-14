# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## What this is

`mux` is a TUI tmux session manager (Go + Bubble Tea) that shows a live preview of every session, detects AI CLIs (`claude`, `codex`, `aider`, `gemini`), and displays git branch / worktree status and Claude token cost.

This repo is a **personal fork**, third in a chain: `lunemis/mux` → `xguru/mux` → this repo. The module path is `github.com/lloydkwon/mux`. Fork-specific features: the "New shell" / "New tmux session" action rows at the top of the list, persistent per-session ordering (`0`–`9`, `o` to rotate sort), and the Claude progress state badges.

원격은 둘이다. `origin` 은 이 포크(`lloydkwon/mux`, SSH)이고, `upstream` 은 이 포크가 갈라져 나온 `xguru/mux` 다. `upstream` 은 원래 저장소의 새 변경을 받아 보는 용도라 HTTPS 로 fetch 만 하고, push 주소는 `DISABLED` 로 막아 두었다(`git remote set-url --push upstream DISABLED`) — 남의 저장소로 가는 push 가 실수로라도 나가지 않게. 새 변경은 `git fetch upstream && git log --oneline HEAD..upstream/main` 으로 본다. 원본 `lunemis/mux` 는 원격으로 두지 않는다. 2026-10-14 에 확인했을 때 두 저장소 모두 이 포크에 없는 커밋이 0개였다(`xguru` 는 07-18, `lunemis` 는 07-07 이후 변경 없음). 이 클론에는 한때 `upstream` 이 빠져 있었으므로, 없으면 `git remote add upstream https://github.com/xguru/mux.git` 로 다시 단다.

Push over SSH — the `gh` OAuth token lacks the `workflow` scope, so HTTPS pushes are rejected for touching `.github/workflows/`. Use `go install ./cmd/mux` to install the working tree; `@latest` installs the published branch instead.

## Commands

```bash
make build                       # → ./mux, injects version via -ldflags
make test                        # go test ./...
make install PREFIX=~/.local     # installs to $PREFIX/bin

go test ./ui -run TestFlattenMenu -v    # single test
go test ./herdr                        # herdr 매핑 — 캡처한 픽스처로 hermetic
go test ./ui -run TestGolden -update  # 화면 골든(ui/testdata/golden)을 다시 쓴다 — 커밋 diff 로 바뀐 화면을 본다
go vet ./... && golangci-lint run       # expected before submitting (no repo config; defaults)
GOARCH=arm64 go build ./cmd/mux         # CI also cross-compiles this

scripts/tui-snapshot.sh --keys 'j Tab'   # 격리 서버에서 작업 트리 빌드를 띄워 화면을 글자로 찍는다 (에이전트용)
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

Every tick is *stateless*: `sessionsLoadedMsg` replaces `m.sessions` wholesale and the previous slice is dropped, and nothing in the TUI compares one tick against the last. The only cross-tick history in the codebase lives in `mux watch`, not here — see [docs/design/panel.md](docs/design/panel.md).


## 설계 문서는 `docs/design/` 에 있다

이 파일에는 모든 세션이 늘 알아야 할 것만 둔다. 기능별 설계 이유(무엇을 실측했고, 무엇을
왜 버렸는지)는 `docs/design/` 에 주제별로 있고, 색인은 [docs/design/README.md](docs/design/README.md) 다.
`tmux/`·`ui/`·`herdr/`·`cmd/mux/` 의 `CLAUDE.md` 가 «이 파일을 고치기 전에 읽을 문서»를 파일별로 가리킨다.

**고치기 전에 해당 문서를 읽는다.** 이 저장소의 코드는 상당수가 «되돌리기 쉬운데 되돌리면
안 되는» 결정이고, 그 이유는 코드가 아니라 문서에 있다. 설계를 바꿨으면 같은 커밋에서 그
문서를 고친다. 새 주제는 새 문서로 만들고 색인과 디렉터리 `CLAUDE.md` 에 한 줄씩 더한다.

### 되돌리기 쉬운 것들 — 한 줄 요약

전부 근거가 문서에 있다. 손대기 전에 괄호 안 문서를 읽는다.

- **`ui` 에는 목 러너가 없다.** tmux·파일·프로세스를 건드리는 일은 `Update` 안이 아니라
  `tea.Cmd` 로 하고, 기능 스위치의 제로값(`src` nil, `exePath` "", `editorBin` "",
  `ghostIdle` 0, `resolveOwn` nil)이 «꺼짐»이어야 한다 — 그래야 `go test ./ui` 가 개발자의
  실제 서버·설정·데스크톱에 닿지 않는다. (panel.md, panel-events.md, panel-width.md, herdr.md)
- **Bubble Tea 루프 안에서 attach·세션 생성을 하지 않는다.** `Update` 는 대상을 적고
  `tea.Quit`, 실행은 `cmd/mux` 가 한다. (tui.md, claude-mode.md)
- **`listFormat` 에 필드를 더하면** `parseLine` 의 `SplitN`(12)·테스트 픽스처·
  `scripts/tmux-assumptions.sh` 의 필드 수를 같이 고친다. `@mux_note` 는 마지막 필드, 하한은 11. (tui.md, note.md)
- **글리프는 `ansi.StringWidth` 와 실제 그려지는 폭이 같아야 한다** — 폭 보정은 불가능하다.
  `TestGlyphWidthsAreStable`. 색은 ANSI 인덱스만(hex·`AdaptiveColor` 금지), 선택 행은 reverse
  video 에 세그먼트 색을 버린다(`TestSelectedRowDropsSegmentColors`). (rendering.md)
- **상태 글리프는 도구 아이콘을 대체한다, 옆에 붙지 않는다** — 판단은 `aiGlyph` 한 곳. (ai-state.md)
- **herdr 행 렌더**: AI 상태 없는 창 행은 바이트 그대로(`TestWindowRowWithoutAIStateIsUnchanged`),
  `ListSessions` 의 «exit status 삼키기»는 복제하지 않는다. (herdr.md)
- **`--claude` 입양은 `--fork-session` 을 쓰지 않고, 순차로, 초 경계를 기다리며** 돈다. 끝나면
  `ResetClaudeRunCache()`. (claude-mode.md)
- **짝 합치기**: `break-pane` 금지, pane 하나뿐인 검토 세션은 거부, `join-pane -d`. (pair.md)
- **패널의 모든 tmux 호출은 `selfPane()` 을 지목한다.** `restoreFocus` 의 `PaneActive` 가드,
  클램프한 폭은 저장하지 않는다(`TestFirstSizeClampDoesNotWriteBack`), `M-Enter` 는 바인딩하지
  않는다(`TestPanelBlockLeavesAltEnterAlone`). (panel.md, panel-width.md)
- **이벤트 로그**: backfill 은 `trimLog` 앞, 아카이브는 trim 전 슬라이스와 합친다
  (`TestArchiveKeepsWhatTrimLogDropped`), 세션당 한 건만 복원. (panel-events.md)
- **서버 되살리기**: 소켓이 있으면 신호하지 않는다, `/proc` 스캔으로 서버를 찾지 않는다,
  `procStart` 를 못 적었으면 물러선다. (revive.md)
- **창의 유일한 pane 인 유령 패널은 죽이지 않고 제목만 지운다** — `kill-pane` 이 곧 `kill-session`. (panel.md)
- **유령 클라이언트**: `StaleClients` 의 네 규칙 전부, `DetachTTY` 의 빈 tty 거부. (panel-width.md)
- **훅 경로는 실패를 말하지 않는다** — `mux panel --auto` 는 삼키고, `mux border` 는 빈 줄에 exit 0.
  사람이 누른 키(`prefix + a`)는 보고한다. (tmux-config.md, border.md)
- **tmux 설정 편집**: 패널 펜스를 팝업의 줄 단위 마커로 만들지 않는다, 생성 줄은 tmux(큰따옴표)와
  `/bin/sh`(작은따옴표) 두 겹으로 인용, 쓰기는 `os.WriteFile`(심링크를 따른다). (tmux-config.md)
- **`modeHelp` 에 서브모델을 만들지 않는다**, 도움말 본문은 `TestHelpBodyFitsPopup` 안에 맞춘다. (tui.md)
- **프로젝트 필터 시딩은 버그가 아니다** — `esc` 가 탈출구다. (tui.md)
- **주석에 «measured»·«verified» 를 쓰면** `scripts/tmux-assumptions.sh` 에 그 단언을 한 줄 더한다.
