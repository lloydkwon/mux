# TUI 목록·입력·모드

> 관련 코드: ui/tree.go, ui/app.go, ui/filter.go, ui/preview.go, ui/target.go, ui/help.go, ui/preferences.go, 서브모델들
>
> 원래 루트 `CLAUDE.md` 의 절이었다(2026-10-14 분리, 본문은 그대로). 본문의 «이 파일»·«위/아래 절»은 `docs/design/` 의 다른 문서일 수 있다 — 색인은 `docs/design/README.md`.

## Flattened tree list model

There is no nested view. `ui/tree.go`'s `flattenMenu` produces a flat `[]listItem` — action rows, then each session, then (if expanded) its windows, then (if expanded) their panes — and `Model.cursor` is an index into that slice. `treeState` holds the expansion sets plus the window/pane caches; expansion survives session refreshes, and `pruneCaches` drops entries for sessions that vanished.

Call `rebuildItems()` after any change to sessions, filter text, ordering, or expansion state, or the cursor will point at a stale row. Action rows are only included in the unfiltered top-level view.

## Click selects, double click commits

Mouse reporting is on (`tea.WithMouseCellMotion()` in `cmd/mux/main.go`), which costs the terminal's own scroll and drag-select — Shift still gets them. A press maps to an item index through `chromeTop()` (title, plus the filter/error bar when there is one), the column's border, and `listOffset` — the *same* function `renderListView` scrolls by, so a row the user can see is a row they can hit. `chromeTop`, `panelHeight`, `listWidth` and `extraBar` exist for that reason: `viewMain` used to compute them inline, and a click map that recomputed them separately would drift the moment the chrome changed.

A single click only moves the cursor. Unlike the watch panel — where a click switches outright because there is nothing to read first — the right column here *is* the thing to read, so clicking to look and clicking to leave must not be the same gesture. The second press on the same row inside `doubleClickWindow` commits, and it commits through `activateCurrent()`, which `enter` also calls: a click that attached somewhere other than where the key would is a bug nobody would think to look for.

## 목록은 프로젝트로 좁혀서 열린다

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
있는데, 그 이유는 [note.md](note.md) 의 "세션 메모는 tmux 옵션에 산다"에 있다.

시딩된 필터에서는 액션 행("새 셸"/"새 세션")을 계속 보여준다(`m.filterText == m.projectDir`).
사용자가 친 필터가 아니라 기본 시야이므로, 여기서 액션이 사라지면 VS Code에서 연
mux는 새 세션을 만들 길이 없다.

## Preview targeting

`previewKey{session, window, pane}` uses `-1` as "the active one"; `.target()` renders it as tmux target syntax (`sess`, `sess:1`, `sess:1.2`). The model caches one preview blob plus the key it belongs to, and `viewMain` only renders it when that key still equals `previewKeyForItem(currentItem)` — otherwise the pane shows blank rather than the wrong session's output.

## Attaching happens after the TUI exits

Never attach from inside the Bubble Tea loop. `Update` records `attachTarget` / `detachRequested` and returns `tea.Quit`; `cmd/mux/main.go` inspects the returned model and then calls `ui.AttachToSession`, which `select-window`/`select-pane` first (attach replaces the process) and then either `switch-client` (inside tmux, `$TMUX` set) or `syscall.Exec` of `attach-session` (outside). "New shell" only detaches when `$TMUX` is set; outside tmux, quitting already returns to the login shell.

## Modal sub-models

`mode` selects which `updateX` handles input. Sub-models (`create`, `rename`, `filter`, `confirmKill`, `order`) are plain structs with their own `Update`/`View`; they report completion by emitting a message (`sessionCreatedMsg`, `sessionRenamedMsg`, `sessionOrderMsg`, …) that the *top-level* `Update` handles before mode dispatch — that is where mode is reset to `modeList` and side effects like preference writes happen.

`modeHelp` is the deliberate exception: it carries no state, so it has no sub-model struct, no `Model` field, and no completion message — just an enum value, a `case` in the mode dispatch that resets to `modeList` on any `tea.KeyMsg`, and `viewHelp` in `View`. Don't add a `helpModel` to make it match the others. It also renders through `fixedBox` instead of `viewWithOverlay`, because a centered box has no size bound and the page is taller than a `mux popup` gets on an 80x24 terminal (68x19) — that budget is pinned by `TestHelpBodyFitsPopup`, and adding a line to `renderHelpBody` will fail it.

## Preferences

`ui/preferences.go` stores sort mode and the `{session name: order}` map in `os.UserConfigDir()/mux/preferences.json`, written atomically (temp file + `chmod 0600` + rename). `normalized()` is the validation gate — it runs on both load and save and drops blank names / non-positive orders. Rename and kill both fix up the `Orders` map so entries don't leak.
