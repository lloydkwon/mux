# 작업 pane 위의 한 줄 (`mux border`)

> 관련 코드: ui/border.go, cmd/mux (runBorder), tmux/setuppanel.go
>
> 원래 루트 `CLAUDE.md` 의 절이었다(2026-10-14 분리, 본문은 그대로). 본문의 «이 파일»·«위/아래 절»은 `docs/design/` 의 다른 문서일 수 있다 — 색인은 `docs/design/README.md`.

## The line above the pane you work in

`pane-border-status top` plus a `pane-border-format` that calls `mux border` (`ui/border.go`, `cmd/mux`'s `runBorder`, installed by `SetupPanel`) puts the session's summary — name, directory, language, tool, live state, branch — on the *other* column, above the shell. It is the only row of that side mux gets to write in: the pane is the user's terminal, and its title already belongs to whatever runs there (Claude Code names its pane after the task in hand, so taking it over would cost more than the line gives).

`BorderLine` breaks three of this codebase's own rules on purpose. It emits **no ANSI**, because tmux styles formats with `#[fg=…]` and paints raw escapes as text — colour is left to `pane-border-style`, and the state glyph carries what a colour would have said. It **does not right-align**, because tmux fills what the format leaves unused with `─`, so padding to the full width would replace that run with blanks and break the border. And it **gives up its parts as it narrows** — branch, then the language, then the tool's name, then the directory — never the session name, since a border that cannot say which session it belongs to has nothing worth drawing.

Two things were measured rather than assumed. `#()` **does** re-run inside `pane-border-format` (verified on an isolated server: renaming a session changed the line without a restart), on the `status-interval` cadence — the manual only documents this for the status line. And the border **costs one row per pane**, the panel's included: measured 20 → 19 for one pane, 20 → 18 with a second work pane.

The format's conditional skips the panel's own pane by matching `panelCommand` against `pane_start_command`. Deciding it in tmux rather than in `mux border` means the panel spawns no process at all, and `mux border` never has to work out which pane it is being asked about. **The line carries two levels of quoting for two parsers**: double quotes for tmux (which is what stops `#` in `#{pane_id}` starting a comment) and single quotes around the path inside `#()`, which `/bin/sh` reads — `TestPanelBlockQuotingKeepsPaneIDInsideQuotes` pins both.

`mux border` is a fresh process per pane per refresh, which is why it uses `tmux.SessionForTarget` rather than `ListSessions`: one `display-message` with list-sessions' own format, handed to the same `parseLine`. It prints an empty line and exits 0 on any failure — an error string here is not reported anywhere a user can act on it, it is simply painted across the top of the pane they are working in and stays there.

## 프로젝트 언어 (2026-10-30)

폴더 바로 옆에 프로젝트의 언어를 적는다(`  ~/dev/projects/my/mux  Go  ⏳ 3m claude  ⌥ main`). «어디»와
«무엇»이 한 묶음이라 그 자리다. 줄이 좁아지면 가지 다음으로 버린다 — 도구의 상태는 지금 일어나는 일이고
언어는 프로젝트의 것이라 상태가 더 오래 남는다(`TestBorderLineShowsTheProjectLanguage`).

- **판별은 `tmux/language.go` 한 곳이다.** 빌드·의존성 파일의 이름만 본다(`go.mod`, `pom.xml`,
  `build.gradle(.kts)`, `pyproject.toml`, `package.json`, `tsconfig.json`, `*.csproj` …). 소스 파일을 세지
  않는 것은 `mux border` 가 pane 마다, 갱신(`status-interval`)마다 새로 뜨는 프로세스라 캐시할 곳이 없어서다 —
  폴더 하나에 `ReadDir` 한 번으로 끝난다. 섞인 폴더는 표의 순서가 정한다: JVM 빌드 파일이 Python 의
  `requirements.txt` 를 이기고, 언어 자신의 빌드 파일이 도구용으로 딸려 오는 `package.json` 을 이긴다.
  JVM 은 `src/main/kotlin` 이 있으면 Kotlin, 아니면 Java. 표에 없는 폴더는 아무것도 적지 않는다.
- **빌드 파일 없는 Python 은 약한 표식으로 잡는다**(2026-11-02). `infra-monitor` 는 `monitor.py`·
  `monitor_app/*.py`·`.venv` 뿐인 Python 도구라 언어가 비었고, 오른쪽 클릭도 PyCharm 이 아니라 IntelliJ 를
  띄웠다. 그래서 빌드 파일 규칙이 모두 빗나간 폴더에서만 맨 위의 `*.py`·`*.pyw` 파일이나 virtualenv
  (`.venv`·`venv` 안의 `pyvenv.cfg` — 이름만 같은 폴더가 아니라 venv·uv 가 만든 것)를 Python 으로 본다.
  약한 표식은 **어떤 빌드 파일에도 진다**: 같은 폴더에서도, 위로 올라가는 동안에도. 약한 표식을 만나도
  올라가기를 멈추지 않고, 저장소 맨 위까지 빌드 파일이 없을 때만 가장 가까운 약한 표식이 답이 된다 —
  Go 저장소의 `scripts/gen.py` 에 들어간 셸은 Go 다(`TestProjectLanguage`).
- **어느 폴더인가:** `@project_dir` 가 있으면 그것, 없으면 pane 의 폴더(`runBorder`). 셸이 `ui/` 같은
  하위 폴더에 들어가 있는 일이 흔해서, 표식이 없으면 **저장소 맨 위(`.git` — worktree 에서는 파일)까지만**
  위로 올라간다(`ProjectLanguage`). 저장소 밖의 폴더는 남의 프로젝트다. **홈은 읽지 않고 거기서 멈춘다** —
  pyenv 의 전역 `.python-version` 하나가 홈 아래 모든 셸을 Python 으로 만든다(`TestProjectLanguage`).
- **`BorderLine` 은 언어를 인자로 받는다.** 디스크를 읽는 것은 `runBorder` 이고 렌더러는 순수하게 남는다.
  `tmux.Session` 에 필드를 두지 않은 것은 매 틱의 `ListSessions` 가 치를 값이 아니어서다 — 언어를 쓰는 곳은
  이 줄 하나다.
- 패널의 PyCharm 규칙(`isPythonProject`, panel.md)도 같은 표를 쓴다. 다만 그쪽은 위로 올라가지 않는다 —
  오른쪽 클릭은 바로 그 폴더를 연다.
