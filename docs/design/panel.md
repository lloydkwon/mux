# 패널 (`mux watch`)

> 관련 코드: ui/watch*.go, ui/notify.go, ui/editor.go, ui/restart.go, tmux/panel.go
>
> 원래 루트 `CLAUDE.md` 의 절이었다(2026-10-14 분리, 본문은 그대로). 본문의 «이 파일»·«위/아래 절»은 `docs/design/` 의 다른 문서일 수 있다 — 색인은 `docs/design/README.md`.

## The sidebar lives in `mux watch`

The panel is a pane on the **right** of the window (`split-window -h`, `tmux/panel.go`). It opened on the left until a phone made the case against it: a client narrower than the window shows the leading columns, so a panel on the left is the half you can see and the shell is the half you cannot. The pane the user types in gets the left edge. `notifyLines` (`ui/notify.go`) renders the whole thing borderless — the tmux pane already draws one — and `watchModel.View` drops it into `fixedBox`.

**The panel's session header is opt-in, and off by default.** `panelHeaderLines` (`ui/notify.go`) draws the selected session's name, branch, state and directory above the list; `@mux_panel_header` (global, `on`/`1`/`true`/`yes`) turns it on, and `PanelHeaderEnabled` (`tmux/panel.go`) is read **once** in `RunWatch`, the same way `MinWindowWidth` is — a preference does not change between ticks.

The default is off because `mux border` now puts the same facts on the top border of the pane you are in, and the panel opens with its cursor on your own session (`autoSelect`), so the header would be a second copy of the line right beside it. What it still answers is the case the border cannot reach: move the cursor with `M-Up`/`M-Down` and it describes a session you are *not* in. That is why it is an option rather than a deletion — the third thing in this panel's history to be removed for costing more chrome than it returned, and the first to be kept behind a switch.

**The panel draws no session's screen, deliberately.** A detail column that previewed the selected session was built and removed: it took half the panel to show a copy of a pane, and the copy you most wanted was usually the real pane sitting right beside it. What the panel shows is what a terminal cannot — which sessions exist, what state each is in, and what they *just* finished. The rest of the window stays your shell.

Because there is nothing to read first, **a click switches**. The keyboard keeps two steps only because `send-keys` cannot point and commit at once: `mux nav up/down` moves `m.selected` and `enter` commits it. That cursor is a session *name*, not a row index — rows are ordered by how long a state has held (`sortByDisplayedAge`), so an index means something different two seconds later. `reselect` re-anchors it every refresh.

**`ownSession`** is resolved in `RunWatch` (`tmux.SessionForPane(selfPane())`). It marks that row `◀` and is where `autoSelect` opens the cursor, so the panel says where you are before it says anything else. An empty `ownSession` means "could not tell" and falls back to the top row.

**It is re-resolved when it stops naming a live session, and that is a fix rather than a nicety** (`ui/watchown.go`). It was resolved *once*, on the reasoning that being wrong "costs a stale marker, not correctness". Measured, it costs the cursor: a `rename-session` leaves the panel holding a name nothing answers to, and then `ownSessionAttached` never matches, so `ownAttached` sticks at `false` and the arrival edge below **can never fire again**; the moment the selected session goes away `autoSelect` drops to the top row. Observed on three live panels at once — renamed sessions drew no `◀` at all and sat on someone else's row until the process restarted, while the one session never renamed was fine.

The check is free: the tick already holds the session list, so a settled panel spends nothing, and only a rename pays the one `display-message` — the rule `adoptSavedWidth` follows when it re-reads the window only where the ceiling would bite. It has to be a `tea.Cmd` for the reason `mergeEventsCmd` and `ghostCheckCmd` are, and `resolveOwn` being nil disables it, which is what keeps every hand-built `watchModel` in the tests off the developer's own server.

**Adopting a fresh name does not move the cursor.** It clears `ownAttached` and lets the arrival edge do it on the next tick, so one mechanism owns "put the cursor where you are" and a cursor moved while the user is away is still left alone. A failed resolve empties `ownSession` instead — the documented "could not tell", which also closes the gate so a pane that can never answer does not ask every two seconds.

`autoSelect` used to *skip* it, reasoning that `enter` there would go nowhere. Both halves were wrong, and the fix is worth not undoing. There is one panel per window, so switching sessions puts you in front of a panel that has never been told anything — skipping meant it opened with an unrelated session highlighted, which is a sidebar failing at the one thing a sidebar is for. And `enter` there is not a no-op: `switchToSession` calls `restoreFocus` before it switches, so on the session you are already in it hands you back to the pane you were working in.

**The cursor re-anchors to `ownSession` when a client *arrives*, and that is an edge, not a state** (`reanchorOnArrival`). `reselect` only re-picks when the selected name has vanished, so a cursor the user moved was otherwise kept forever — and the one reason to move it is to *leave*, which meant the panel you came back to was still pointing at the session you went to, `◀` and the highlight naming different rows with nothing to reconcile them. The signal is `Session.Attached` for the pane's own session, which `ListSessions` already carries every tick, so it costs no extra call. It has to be the `false → true` transition: "my session is attached" is true the whole time you sit in it, so acting on the level would drag the cursor back two seconds after every `M-Up`. What it cannot tell is *which* client arrived — with two clients on one server, a second one landing here re-anchors this cursor too.

Blank lines in the session column are layout, not decoration: a session's trailing blank carries that session's name, which is what makes a click target two rows tall. Group and section breaks carry none, so they do not stretch the block above them.

**`restoreFocus` is guarded by `PaneActive`, and the guard is load-bearing.** tmux's `MouseDown1Pane` runs `select-pane` before forwarding a click, so after a click the panel *is* active and `select-pane -l` is exactly the undo — without it, the window keeps reporting the panel's directory as its session's own (`tmux/panel.go`'s `-c` comment). Keys arrive by `send-keys` and never make it active, and restoring there selects whatever the window visited before the pane the user is in. Measured: `enter` from a three-pane window moved focus to the third pane.

## 패널의 두 버튼: 왼쪽은 전환, 오른쪽은 에디터

패널에서 **왼쪽 클릭은 세션 전환**이고 **오른쪽 클릭은 그 세션의 프로젝트 폴더를
VS Code 로 여는 것**이다. 오른쪽 클릭은 **전환하지 않는다** — 지금 일하던 터미널을
떠나지 않고 다른 프로젝트의 에디터만 여는 것이 이 제스처가 있는 이유이고, 옮기고
싶으면 왼쪽 버튼이 바로 옆에 있다.

**VS Code 를 «띄우는» 것은 오른쪽 클릭뿐이다. 전환은 «이미 열린» 창만 앞으로 낸다.**
왼쪽 클릭·`enter`·`mux nav enter`, 그리고 TUI(`mux`)에서 세션을 고르는 것은 모두
`raiseOpenEditor`(`ui/watch.go`) 한 곳을 거친다 — 터미널을 옮기고, 그 세션의 VS Code
창이 이미 열려 있으면 그 창을 앞으로 내고, 없으면 아무것도 하지 않는다. 터미널과
에디터는 한 세션의 두 화면이라 하나만 옮기면 다른 프로젝트를 읽으며 타이핑하게 된다.
열린 창은 두 가지로 안다: 그 창의 통합 터미널이 세션에 붙어 있거나(`VSCodeClientDirs`),
VS Code 자신의 창 기록(`storage.json`, `tmux/vscodewindows.go`)에 그 폴더가 있거나.

TUI 는 한때 이 규칙의 «이미 열린» 절반 없이 자기 사본을 갖고 있어서, 세션을 고를
때마다 VS Code 가 없으면 새로 띄웠다(2026-10-15). 그 버그를 고친다며 전환에서 에디터를
통째로 뺀 적이 있는데 그건 틀렸다 — 오른쪽 클릭으로 띄운 창을 왼쪽 클릭이 앞으로 내는
것이 원래 기능이다. 고칠 곳은 TUI 가 같은 함수를 쓰게 하는 것이었다.

네이티브 리눅스의 VS Code 는 창 기록을 `~/.config/Code/User/globalStorage/storage.json` 에
`file://` 폴더로 쓴다(WSL 에서는 Windows 쪽 파일의 `vscode-remote://wsl+…` 폴더만 센다).
VS Code 는 끝날 때도 다음에 되살릴 창 목록을 그 파일에 남기므로, 네이티브 기록은
VS Code 프로세스(`comm` 이 `code`)가 떠 있을 때만 믿는다 — 아니면 VS Code 가 꺼진
동안의 왼쪽 클릭이 VS Code 를 띄운다. `TestOpenVSCodeFoldersIgnoresStorageWhenVSCodeIsNotRunning`.

**네이티브 Wayland 에서는 `code <폴더>` 가 창을 앞으로 내지 못한다.** VS Code 는 그 폴더의
창에 포커스를 요청하지만, GNOME(Mutter)은 활성화 토큰이 없는 요청을 버린다. 클릭은
터미널 → tmux → 패널 프로세스를 거쳐 `code` 에 닿으므로 토큰이 남아 있을 수 없다. WSL 에서
이 기능이 동작한 것은 Windows 가 그 요청을 들어줬기 때문이다(2026-10-15 네이티브로 옮긴 뒤
water-ai 를 눌러도 창이 오지 않았다). `wmctrl`·`xdotool` 은 X11 창만 다루고 VS Code 는
Wayland 로 돈다(`--ozone-platform=wayland`, 한글 입력 때문에 바꿀 수 없다).

그래서 창은 GNOME Shell 안에서 올린다(`ui/windowfocus.go`). dotfiles 의 `window-focus@local`
확장이 세션 버스에 `List`(VS Code 창의 id·제목, JSON)와 `Activate(id)` 를 내놓고, mux 는
`busctl` 로 목록을 받아 **폴더의 창을 Go 에서 고른다** — 고르는 규칙을 테스트하려고 확장이
아니라 여기 뒀다(`TestPickEditorWindow`). VS Code 기본 제목 `… - <폴더 이름> - Visual Studio
Code` 의 **폴더 칸만** 보고 그 칸과 **같아야** 한다 — 부분 문자열이면 mux 를 찾다 my-mux
창을 올리고, 다른 칸(열린 파일 이름)까지 보면 `doublecmd` 라는 파일이 열린 창이 doublecmd
프로젝트 행을 가로챈다. 같은 이름의 폴더 창이 둘이면 고르지 않는다(엉뚱한 프로젝트를
올리느니 안 올린다). 프로필 이름이 낀 제목은 맞지 않아 `code` 로 물러선다.

결과는 셋이고 섞으면 원칙이 깨진다(`focusResult`): **올림**, **창 없음**(shell 이 답했고
그 폴더의 창이 하나로 정해지지 않음), **물을 수 없음**(GNOME Shell 이 없음 — WSL·다른
데스크톱, 실측 2ms 에 실패). 왼쪽 클릭은 «물을 수 없음»일 때만 `code <폴더>` 로 물러선다.
«창 없음»은 창 기록이 낡았다는 답이라, 거기서 `code` 를 부르면 왼쪽 클릭이 창을 띄운다.

**GNOME 은 있는데 확장만 없으면 «창 없음»이다.** 화면이 잠기면 GNOME 이 확장을 끄고(`State:
INACTIVE`), `List` 는 «그런 객체가 없다»로 실패한다. 이것을 «물을 수 없음»으로 읽던 때는
밖에서 ssh 로 쓰는 동안(집 화면은 잠김) prefix+m 에서 세션을 고를 때마다 `code <폴더>` 가
돌았다 — 실측 1.04초, 그동안 팝업은 대체 화면을 벗어난 빈 사각형으로 남았다(2026-10-16,
격리 서버의 같은 흐름은 대체 화면 해제부터 종료까지 24ms). Wayland 라 `code` 로는 창이
올라오지도 않으니 기다릴 것이 없다. 구분은 오류 문구가 아니라 버스에 묻는다 —
`NameHasOwner("org.gnome.Shell")`. 그 문구는 shell 안의 GLib 이 만들고 번역될 수 있다.
`TestFocusShellWindowWithoutExtension`.
오른쪽 클릭은 그 폴더에 **정확히** 열린 창이 있다고 알려졌을 때만 shell 에 묻고, 아니면
바로 띄운다 — shell 은 이름으로만 고르므로, 창 없는 `~/personal/api` 를 누르면 `~/work/api`
창이 올라오고 아무것도 안 뜬다(`TestRightClickOnAFolderWithoutAWindowDoesNotAskTheShell`).
확장의 목록은 VS Code 창으로만 한정한다: 세션 버스의 누구나 부를 수 있어서, 모든 창 제목을
내놓으면 GNOME 이 `Introspect` 로 막아 둔 것을 열어 주는 셈이다.

이 스위치도 제로값이 꺼짐이다: `watchModel.focusWindow` 가 nil 이면 shell 을 부르지 않는다.
`RunWatch` 와 TUI 의 `raiseEditorFor` 만 `focusShellWindow` 를 넘긴다.

**옵션이 없는 것이 결정이다.** 처음에는 `@mux_panel_editor` 뒤에 두고 모든 클릭이
에디터를 열게 했는데, 옵션이 막으려던 것은 "훑어보려고 누른 클릭에 창이 뜨는 것"
하나였다. 버튼으로 가르면 그 위험 자체가 없어진다 — 오른쪽 버튼은 실수로 눌리지
않는다. 그래서 제스처가 opt-in 이고, 옵션은 지웠다. 되돌리고 싶어지면 그게 무엇을
막아 주는지부터 물을 것.

**`@mux_editor` 는 그 옵션이 돌아온 것이 아니다**(2026-10-21). 지운 옵션은 클릭이 편집기를
*여느냐*를 막았고, 이것은 *무엇으로* 여느냐만 고른다 — 없거나 `code` 면 VS Code, `idea` 면
IntelliJ IDEA. 제스처가 opt-in 이라는 결정은 그대로다. 오른쪽 클릭마다 `tmux.EditorChoice`
로 읽으므로 `tmux set -g @mux_editor idea` 가 이미 떠 있는 패널에도 바로 닿는다. 고른
편집기를 못 찾거나 모르는 값이면 VS Code 로 물러서지 않고 로그에 남긴다 — 물러서면 설정이
먹었는지 알 길이 없다(`TestRightClickReportsAnEditorItCannotRun`).

**창 올리기도 `@mux_editor` 를 따른다**(2026-10-22, 사용자가 VS Code 대신 IntelliJ 를 쓰기로 함).
왼쪽 클릭·`enter`·TUI 의 선택은 `raiseChosenEditor` 를 거친다: `idea` 면 그 세션의 프로젝트가 열린
IntelliJ 창을 올리고(VS Code 창은 건드리지 않는다), 그 밖은 예전 그대로 VS Code 다. 띄우는 것은
여전히 오른쪽 클릭뿐이다 — IntelliJ 쪽에는 `code` 같은 물러설 곳도 없다(`idea` 는 올리는 게 아니라
여는 런처다). `TestLeftClickRaisesTheChosenEditor`.
- **창 고르기:** 확장이 IntelliJ 창(WM_CLASS `jetbrains-idea`)에 `app: "idea"` 를 달아 내놓는다.
  IntelliJ 제목은 «프로젝트이름 – 앞에 있는 것»(en dash, 실측 `mux – Claude Code`)이라 첫 토막만
  같음으로 본다(`pickIntelliJWindow`). VS Code 규칙은 IntelliJ 창을 보지 않는다.
- **첫 토막에 경로가 붙은 창은 경로로 고른다**(2026-10-28). IntelliJ 는 같은 이름의 프로젝트가 열린
  적이 있으면 그 토막을 «이름 [경로]» 로 쓰고, 경로는 홈 아래면 `~/…` 로 줄인다(그날 water_ai 창:
  `water_ai [~/dev/projects/si/water_ai] – Local`, 열린 프로젝트 중 이름이 겹치는 것은 없었다 — 한번 붙은
  경로가 남는다). 이름만 보던 때는 이 창이 어떤 폴더와도 같지 않아 왼쪽 클릭이 아무것도 올리지 못했다.
  경로는 폴더를 가리키므로 그 경로가 세션 폴더와 **같을 때만** 고른다 — 이름이 같은 다른 폴더의 창은
  고르지 않는다. 홈을 모르면 `~` 경로는 어떤 폴더와도 같지 않다
  (`TestPickIntelliJWindowReadsThePathInTheTitle`, `TestPickIntelliJWindowWithoutAHome`).
- **열린 것이 확인된 폴더만 묻는다:** 창은 이름으로만 고를 수 있어서, 그 세션의 IntelliJ 터미널
  (JediTerm 클라이언트의 `PWD`, `IntelliJClientDirs`)이나 IntelliJ 의 최근 프로젝트 기록에서
  `opened="true"` 인 폴더(`tmux/intellijprojects.go`)가 세션 폴더를 소유할 때만 묻는다 — 아니면 같은
  이름의 다른 프로젝트 창이 올라온다(`TestLeftClickRaisesNoIntelliJWindowThatIsNotOpen`). 그 기록은
  IntelliJ 를 끈 뒤에도 «다시 열 프로젝트»로 남을 수 있어 `idea` 프로세스가 떠 있을 때만 믿는다.
- **오른쪽 클릭**도 그 폴더에 정확히 열린 프로젝트면 창을 올리고 끝낸다. 올라오지 않으면 띄운다
  (`TestRightClickRaisesAnOpenIntelliJProjectFirst`).
- 확장 코드를 바꾸면 GNOME Shell 이 다시 읽어야 한다(X11: Alt+F2 → `r`, Wayland: 다시 로그인).
  확장과 mux 중 어느 쪽이 먼저 바뀌어도 깨지지 않는다 — `app` 이 없으면 VS Code 로 읽는다.

**`idea` 는 Python 프로젝트를 PyCharm 으로 연다**(2026-10-30, 사용자가 PyCharm 을 들임). `@mux_editor` 에
셋째 값을 더하지 않았다 — PyCharm 은 따로 고르는 편집기가 아니라 «JetBrains 로 열기»의 한 갈래다.
- **띄우기는 프로젝트 종류로**(`jetbrainsLauncher`, `ui/editor.go`): 폴더 **맨 위**에 `pyproject.toml`·
  `setup.py`·`setup.cfg`·`requirements.txt`·`Pipfile`·`uv.lock`·`poetry.lock`·`.python-version` 중
  하나가 있고 JVM 빌드 파일(`pom.xml`, `build.gradle(.kts)`, `settings.gradle(.kts)`)이 없으면 PyCharm.
  섞인 저장소를 IntelliJ 에 두는 것은 Gradle 저장소의 스크립트용 `requirements.txt` 하나로 Java 를
  못 읽는 IDE 가 뜨지 않게다. 하위 폴더는 보지 않는다 — `water_ai` 처럼 `ai/`·`backend/`·`frontend/`
  를 품은 저장소는 한 언어의 프로젝트가 아니다(`TestIsPythonProject`). 띄우는 법은 IntelliJ 와 같은
  `startDetached` 다: Toolbox 의 `apps/pycharm/bin/pycharm` 은 IDE 를 자기 프로세스 안에서 돌린다
  (실측, 프로세스 이름 `pycharm`).
- **제로값이 꺼짐이다:** `pycharmBin` 이 비어 있으면 폴더를 읽지도 않고 IntelliJ 다
  (`TestRightClickWithoutPyCharmNeverAsksWhatTheProjectIs`). Python 인데 PyCharm 이 없을 때 IntelliJ 로
  여는 것은 위의 «물러서지 않는다»와 어긋나지 않는다 — 사용자가 고른 `idea` 는 지켜졌다. 거꾸로
  IntelliJ 가 없어도 PyCharm 으로 열 Python 프로젝트는 연다.
- **올리기는 JetBrains 창 전체에서:** 확장이 PyCharm 창(WM_CLASS `jetbrains-pycharm`, 실측)에
  `app: "pycharm"` 을 달고, `pickIntelliJWindow` 는 `idea`·`pycharm` 을 한 무리로 고른다. 제목 모양이
  같고(실측 `WelcomeScreen – Welcome to PyCharm`), IntelliJ 로 열어 둔 Python 프로젝트도 계속 올라와야
  한다. 같은 이름이 두 IDE 에 하나씩 열려 있으면 창이 둘이라 답하지 않는다
  (`TestPickIntelliJWindowIncludesPyCharm`). 오른쪽 클릭은 올리기를 먼저 해 보고, 못 올렸을 때만 어느
  IDE 로 열지 묻는다(`TestRightClickRaisesAnOpenProjectBeforeChoosingAnIDE`).
- **열린 프로젝트 기록도 둘:** `tmux/intellijprojects.go` 가 `PyCharm*/options/recentProjects.xml` 도
  읽는다. 각 기록은 **그 IDE 의 프로세스가 떠 있을 때만** 믿는다 — IntelliJ 가 떠 있다고 PyCharm 이
  남긴 «다시 열 프로젝트»를 믿지 않는다(`TestOpenIntelliJProjectsTrustsEachRecordOnlyWhileItsIDERuns`).
  PyCharm 터미널도 JediTerm 이므로 `IntelliJClientDirs` 가 그대로 센다고 보지만, 재 보지는 않았다.
- PyCharm 에도 신뢰 대화상자가 있다. Trusted Locations 는 IDE 마다 따로라 PyCharm 에도 넣어야 한다.

**IntelliJ 의 터미널은 VS Code 의 것과 같은 «편집기 클라이언트»다**(2026-10-21). IntelliJ 로 연
프로젝트의 터미널이 그 세션에 붙으면(아래 dotfiles), 그 tmux 클라이언트는 한 프로젝트를 보는
편집기 창이라는 점에서 VS Code 터미널과 다를 것이 없다. 그래서 같은 대접을 받는다: 창 맞춤이
패널을 그 화면에서 빼고(`tmux/viewport.go`), 왼쪽 클릭의 전환이 끌고 가지 않고(`SwitchWTClient`),
유령 클라이언트로 끊지 않고(`isOwnClient`), `--auto` 가 그 터미널만 보는 세션에 패널을 열지 않고,
TUI 는 프로젝트로 좁혀 연다. 판별은 `isEditorClient` 하나다. 표식은 `TERMINAL_EMULATOR=JetBrains-JediTerm`
이다(JediTerm 은 `TERM_PROGRAM` 을 남기지 않는다). 예외는 `VSCodeClientDirs` 하나다: 그 폴더로
VS Code **창**을 찾아 올리므로 좁은 `isVSCodeClient` 를 쓴다(`TestEditorClientMarkers`).

터미널을 세션에 붙이는 것은 mux 가 아니라 dotfiles 의 몫이다. VS Code 의 짝은 이렇다.
- IntelliJ 의 셸 경로 = `tmux-project`: `options/terminal-local.xml` 의 `shellPath`
- `tmux-autoterm` 확장 자리에는 IntelliJ 시작 스크립트(`extensions/com.intellij/startup/`)를 둔다. 프로젝트가 열릴 때마다 돌아서, 그 폴더의 세션이 있으면(`tmux-project --has`) Terminal 도구 창을 연다.

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

**`mouse off` 에서는 클릭이 «우연히» 도착한다 — 그래서 패널이 그걸 말한다.** `mouse` 가
꺼져 있으면 tmux 는 바깥 터미널의 마우스 보고를 **활성 pane 의 마우스 모드**에 맞춰 켜고
끄고, 들어온 이벤트는 바인딩 없이 포인터 아래 pane 에 넘긴다. 활성 pane 이 claude(마우스를
켠다)인 창에서는 터미널이 클릭을 보내고 패널이 받는다. 활성 pane 이 셸인 창에서는 터미널이
아무것도 보내지 않아, 패널 행을 눌러도 터미널의 글자 선택만 시작된다. 실측이고
`scripts/tmux-assumptions.sh` 13절이 지킨다. 2026-11-01 에 «AI 세션에서는 클릭이 되는데 셸
세션에서는 안 된다»로 보고됐다 — 첫 실행 설정으로 깐 사용자는 `mouse off` 경고를 본 적이
없었다(tmux-config.md).

mux 는 `mouse` 를 켜지 않는다(`MouseEnabled` 주석). 대신 패널이 꺼진 동안 목록 맨 위에
세션 없는 한 줄(` 마우스 꺼짐 · set -g mouse on`, `ui/watchmouse.go`)을 그린다. 세션이 없으니
`sessionAtRow`·`sessionOrder` 가 같은 슬라이스를 읽어도 클릭 행과 커서는 어긋나지 않는다.
묻는 것은 `probeMouse`(nil 이면 꺼짐 — 손으로 만든 `watchModel` 은 서버에 묻지 않는다)를
시작할 때 한 번, 그 뒤 유령 클라이언트 검사와 같은 1분 간격으로. 옵션은 설정을 고칠 때나
바뀌는 것이라 2초 틱마다 창마다 `show-options` 를 부를 이유가 없고, 그 1분이 `mouse on` 뒤에
안내가 남아 있는 최대 시간이다.

**키보드는 뜻이 하나다.** `enter` 와 `mux nav enter`(= `prefix + Enter`)는 왼쪽 클릭과
같이 전환만 한다. 오른쪽 버튼에 해당하는 키는 없다 — 필요해지면 `navKeys`·`navBinds`·
`handleKey`·`cmd/mux` 를 다 고치고 사용자가 `mux setup-panel` 을 다시 돌려야 하므로,
그 값을 치를 이유가 생기기 전까지는 두지 않는다.

**스위치는 `editorBin`·`ideaBin`·`editorChoice` 셋이고, 셋 다 제로값이 off 다.** `RunWatch` 가
시작할 때 `findEditor()`·`findIntelliJ()` 로 두 경로를 한 번 채우고, `editorChoice` 에
`tmux.EditorChoice` 를 넘긴다. VS Code 경로는 `editorBin` 이 비어 있으면 no-op 이고(예전
그대로), IntelliJ 를 골랐는데 `ideaBin` 이 비어 있으면 보고한다. 그래서 관문은 `editorDirFor`
가 아니라 `openSessionEditor` 의 각 분기에 있다 — 폴더 단계에서 막으면 그 보고가 닿지
못한다(`TestEditorDirDoesNotDependOnAnEditor`). `editorChoice` 가 nil 이면 묻지 않고 VS Code 다. 그래서 `watchModel{...}` 을 손으로 만드는 기존 테스트들이
아무것도 실행하지 않고 tmux 에도 묻지 않는다 — `exePath` 가 자가 재시작을 끄는 것과 같은
방식이고, 이게 `go test ./ui` 를 개발자의 데스크톱에서 떼어 놓는다.

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

**IntelliJ 는 그 반대여야 한다** (`startDetached`). `idea` 는 `code` 같은 «넘기고 돌아오는»
런처가 아니다. 2026-10-21 이 기계(Toolbox 2026.2.3, XWayland)에서 오른쪽 클릭과 같은
조건(TMUX 를 뺀 env, setsid, 표준 입출력 막음)으로 재 보니:
- 떠 있는 IntelliJ 에 처음 여는 폴더를 넘기면 «Trust and Open Project?» 대화상자가 **현재 창
  뒤에** 뜨고(`_NET_WM_STATE_DEMANDS_ATTENTION`), 명령은 그걸 닫을 때까지 131초 동안 돌아오지
  않았다(닫은 뒤 종료 코드 1).
- 꺼져 있으면 `bin/idea` 프로세스가 곧 IDE 본체다 — JVM 이 런처 프로세스 안에서 돈다
  (떠 있던 `idea` 프로세스 하나가 1.4GB 를 쥐고 있었다).

그래서 `startEditor` 로 띄우면 앞의 경우는 10초 뒤 «실행 실패»를, 뒤의 경우는 **IDE 를
SIGKILL** 한다. `startDetached` 는 context 없이 띄우고 2초(`detachGrace`)만 지켜본다 — 그 안에
실패로 끝나면 보고하고, 아니면 손을 뗀다. DISPLAY 없는 콜드 스타트는 0.39초 만에 종료 코드
3 으로 끝났다(별도 config·system 경로로 띄워 실측) — 2초면 넉넉하다. `Wait` 는 고루틴이
맡아 좀비를 거둔다. stderr 는 받지 않는다: 버퍼는 며칠 사는 IDE 의 출력을 패널 메모리에
쌓고, 파이프는 IDE 의 stderr 를 패널의 수명에 묶는다. 이유는 IDE 로그(`idea.log`)에 있다.
`TestStartDetachedLeavesALongLaunchRunning` 이 «유예가 지나도 죽이지 않는다»를 지킨다.
숨은 신뢰 대화상자는 mux 가 고칠 수 없다 — IntelliJ 의 Trusted Locations 에 프로젝트 상위
폴더를 넣어 두는 것이 답이다.

**편집기의 화면·입력기 변수는 데스크톱 세션에서 온다** (`editorEnv`, `ui/editor.go`). 패널이 띄운
편집기는 패널의 환경을 물려받고, 그 환경은 패널이 생길 때의 tmux 서버 환경이다 — 패널은 며칠
산다. 2026-10-21: 12:56 에 생긴 my-mux 패널에는 그 뒤 dotfiles 의 `environment.d` 에 더한
`XMODIFIERS=@im=ibus` 가 없었고, 그 패널이 콜드 스타트한 IntelliJ(X11 툴킷, XIM 으로만 ibus 에
붙는다)는 터미널에 한글이 한 글자도 안 들어갔다. GNOME 메뉴로 띄운 앱이 받는 값(systemd 사용자
매니저·gnome-shell 의 환경)에는 있었다. 그래서 `desktopEnvVars`(화면·D-Bus·입력기)만
`systemctl --user show-environment` 의 값으로 덮고 나머지는 패널 것을 둔다 — WSL 의 `code` 셤은
패널의 `WSL_*` 가 있어야 돌고, systemd 가 없으면 패널 환경 그대로다
(`TestWithDesktopEnv`, `TestStartDetachedPassesTheDesktopInputMethod`). VS Code 도 같은 길을
타지만, 이미 떠 있으면 `code` 가 넘기기만 하므로 이 차이는 콜드 스타트에서만 보인다.

`Setsid` 는 패널 pane 이 닫힐 때 반쯤 뜬 에디터가 같이 죽지 않게 한다 — tmux 는 pane 의
프로세스 그룹에 신호를 보내고, 셤이 진짜 애플리케이션에 넘기기 전까지는 아직 우리
그룹에 있다. stdin/stdout 은 `nil`(=`/dev/null`)이어야 한다: 패널은 alt-screen TUI 라
자식이 한 줄만 써도 목록 위에 남는다. stderr 만 받아 `firstLine` 으로 한 줄로 줄여
에러에 붙인다(`tmux/runner.go` 의 `withStderr` 와 같은 이유, 폭이 40칸이라는 것이 추가
이유).

**실패는 `m.local` 에만 넣는다.** 이 pane 의 실행이 실패한 것이지 다른 창의 소식이
아니다 — `switchFailedMsg` 가 공유 로그에 가지 않는 것과 같은 규칙이다.

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
