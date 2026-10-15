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

**VS Code 를 여는 곳은 오른쪽 클릭 하나뿐이다.** 왼쪽 클릭·`enter`·`mux nav enter`,
그리고 TUI(`mux`)에서 세션을 고르는 것은 터미널만 옮긴다. 한때는 전환이 그 프로젝트의
VS Code 창을 함께 앞으로 냈다(TUI 는 창이 없으면 새로 띄우기까지 했고, 패널은 열린
창만 — 붙은 통합 터미널이나 Windows 쪽 `storage.json` 의 창 목록으로 찾았다). «터미널과
에디터는 한 세션의 두 화면»이라는 이유였지만, 실제로는 세션을 고를 때마다 원하지 않은
에디터가 앞을 가렸다(2026-10-15 사용자 요청으로 제거). 창 목록을 읽던
`tmux/vscodewindows.go` 도 그 일에만 쓰여 같이 지웠다. 전환에 에디터를 다시 붙이고
싶어지면, 오른쪽 클릭이 이미 그 일을 한다는 것부터 볼 것.

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
