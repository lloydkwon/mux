# AI 배지와 Claude 상태·비용

> 관련 코드: tmux/aitools.go, tmux/claude_status.go, tmux/claude_activity.go, tmux/claude.go, tmux/proc.go, ui/status.go, ui/list.go
>
> 원래 루트 `CLAUDE.md` 의 절이었다(2026-10-14 분리, 본문은 그대로). 본문의 «이 파일»·«위/아래 절»은 `docs/design/` 의 다른 문서일 수 있다 — 색인은 `docs/design/README.md`.

## One AI badge, two facts

AI CLI detection is a single feature, not a generic layer with a Claude feature bolted beside it. `tmux/aitools.go` owns all of it: `aiToolMap` (which tools exist, their icon and color) *and* `AIState` (`None`/`Working`/`Approval`/`Ready`) with `AIState.Icon()` (`⏳`/`❗`/`✅`).

**The rule: a live state glyph replaces the tool icon, it never sits beside it.** `ui/status.go`'s `aiGlyph` is the one place that decides, and `ui/list.go`, `ui/preview.go`, and `mux status` all go through it. A separate state column would repeat what the tool icon already says, since only a detected AI CLI can have a state. Keep it that way when adding anything state-related.

The badge cell is padded to `badgeWidth` (2) even when empty, because state glyphs measure 2 and tool icons measure 1 — that padding is what keeps the git branch in one column across every row.

**The list is a table, and its columns are decided once per render.** `renderListView` computes `sessionNameWidth` and `anyOrdered` over the *whole* list rather than the visible slice, then hands both to every row: a column sized per row, or per screenful, would move the names as you scrolled. The name leads because it is what the eye looks for; the age, badge and branch follow it in fixed columns so the list is scanned down rather than read across, and the branch is flush right and is the first thing to yield when the row runs out of room.

Two markers became colours instead of columns, which is what freed the gutter from twelve cells to two. The attached session is its name in `colorAccent` (`nameColor`), the active window or pane likewise (`activeColor`) — a marker column costs every row a cell to say something about one of them. The `#3` order label is drawn only when some session in view carries an order.

The list's elapsed column shows the *state's* age for a session with live state (`sessionAge`) and the session's creation age otherwise, and **the recent sort keys off that same value** — `sortedSessions`' default branch, matching the panel's `sortByDisplayedAge`. It used tmux's `session_activity` and that is a different clock: measured on a live server, activity ran 40s → 53s → 1m → 52m in order while the column beside it read 39s, 1m, 28h, 5m, so a correctly sorted list looked unsorted. The column takes the state color — with the glyph folded into the badge, that color is what still marks a blocked row at a glance.

(이 절은 한때 `session_activity` 가 «pane 이 무언가 그렸다는 뜻» 이라고 적었는데 틀렸다. 2026-11-03 격리 서버에서 3.8·3.4 모두 pane 출력과 `send-keys` 로는 움직이지 않았고, 붙기·`switch-client`·클라이언트의 키 입력으로만 움직였다 — «사람이 마지막으로 그 세션에 있던 때» 다. `scripts/tmux-assumptions.sh` 16절이 출력 쪽을 단언한다. 그래도 recent 가 이것을 쓰지 않는 이유는 위의 두 시계 문제 그대로다.)

**«상태의 나이» 는 화면 넷이 한 함수로 센다 — `stateSince` (`ui/status.go`).** 목록 행, 미리보기 머리줄(`aiStatusText`), 패널 행(`panelStateText`), pane 테두리(`BorderLine`)가 모두 이것을 쓰고, `sessionAge` 도 이것 위에 있다. idle 인 Claude 는 상태 파일의 `statusUpdatedAt`(`AISince`)이 아니라 대화 기록의 마지막 작업 시각(`AILastActive`)이다. 프롬프트에서 대화상자를 여는 로컬 명령은 `idle → waiting(dialog open) → idle` 을 실제로 거쳐 `statusUpdatedAt` 을 새로 쓴다. 2026-11-03 에 다른 세션의 Claude 가 테마를 바꾸려고 쉬던 세션마다 `/theme` 을 `send-keys` 로 보냈고, 전날 끝난 다섯 세션이 전부 «9분 전에 끝남» 이 되어 recent 가 보낸 순서대로 섰다. 기록에서 그 세션들의 마지막 작업은 18시간~4일 전이었다.

- **마지막 작업 줄** (`isWorkRow`, `tmux/claude_activity.go`): 메인 대화의 `assistant` 줄, 그리고 meta 가 아니고 `<command-name>`·`<local-command-…>`·`<bash-…>` 로 *시작하지* 않는 `user` 줄(프롬프트, `tool_result`, 중단 표시). `system` 줄(`away_summary`, `turn_duration`)은 세지 않는다. 태그는 머리만 본다 — 프롬프트 본문에 태그를 인용하는 일은 있다.
- **`AISince` 를 덮어쓰지 않는다.** 공유 이벤트 기록(`duplicateAt`, `tmux/eventlog.go`)은 «모든 프로세스가 같은 값을 읽는다» 는 전제로 (세션, 상태, since) 를 이벤트의 열쇠로 쓴다. 기록을 읽은 순간에 따라 달라질 수 있는 값을 거기 넣으면 패널끼리 이벤트가 겹친다. 턴 길이(`turnLength`)와 같은 상태의 새 프롬프트(`newPrompt`)도 `AISince` 다.
- **`statusUpdatedAt` 보다 늦지 않다.** 기록이 더 늦은 줄을 말하면 상태 파일이 아직 따라오지 못한 새 턴이다. 기록이 없거나 못 읽으면 0 이고 화면은 `AISince` 를 쓴다. raw `idle` 일 때만 채운다 — shell 에서 강등된 Ready 의 `statusUpdatedAt` 은 shell 이 시작된 때라 견줄 것이 다르다.
- **한계**: 작업 줄이 하나도 없는 대화(띄우고 아직 아무것도 안 시킨 세션, `/clear` 직후)는 견줄 것이 없어 여전히 `AISince` 다 — 거기에 `/theme` 이 들어가면 그 세션은 «방금» 이 된다. 응답이 한 줄도 붙지 않은 슬래시 명령(`<command-name>` 줄만 남은 스킬 호출)도 작업으로 세지 않는다. 읽기 실패는 캐시하지 않는다 — «작업 줄 없음» 으로 굳으면 쉬는 세션은 기록이 다시 바뀌지 않아 끝내 풀리지 않는다(`TestLastWorkAtDoesNotCacheAReadFailure`).
- **비용**: 기록은 수십 MB 까지 크다(관측 최대 39MB). 끝에서 64KB 부터 읽고 못 찾으면 네 배씩 넓혀 16MB 에서 멈춘다(실제 기록 8개에서 마지막 작업 줄은 끝에서 2~50KB). 파일의 크기·수정 시각이 그대로면 앞의 답을 다시 써서, 1초 TTL 의 상태 스캔마다 드는 것은 stat 하나다.
- `--claude` 목록(`parseClaudeRun`)도 같은 규칙이다. `mux status --json` 의 `since` 는 위젯이 읽는 공개 표면이라 상태 파일 값 그대로다.

## Claude live-state chain

Claude is the only tool that publishes live state, so `tmux/claude_status.go` is the sole producer of a non-zero `AIState`. It reads `~/.claude/sessions/*.json` — Claude Code's own state file, one per running session — and indexes them by tmux session name via each file's `tmux` field (`"<session>:@<win>.%<pane>"`). `ClaudeStatuses()` does one `os.ReadDir` per refresh behind a 1s TTL cache, and `ListSessions` hoists that single call out of its parse loop, so state arrives atomically with the session slice and needs no tick fan-out or `Model` cache.

Four things about this data are easy to get wrong:

- **The file is written only on status change, so mtime is not a heartbeat.** A crashed session leaves its file behind and would display as permanently busy. Liveness is `processAlive` (`tmux/proc.go`): signal-0 for existence, plus a best-effort `/proc/<pid>/stat` field-22 comparison against the file's `procStart` to catch PID reuse. The `/proc` half no-ops on macOS by design — no build tags.
- **`status: "waiting"` means blocked on the user**, whatever `waitingFor` says; a finished turn sitting at the prompt reports `idle` instead. That mapping lives in `mapClaudeState`.
- **`status: "shell"` outranks both `idle` and `busy`, and it is sticky.** Claude reports it for as long as *any* background shell is alive, so a session that left a dev server running never reaches `idle` — its turns end invisibly and anything watching for a turn to finish waits forever. Measured: a session actively generating output reported `shell` for 13 minutes because one server was up. `demoteServerShell` (`tmux/claude_status.go`) recovers the common case by reading the Claude-owned children under `/proc/<pid>/task/<pid>/children` (identified by `shell-snapshots/` in their cmdline, which excludes MCP servers) and promoting to `AIStateReady` when *every* one of them matches `serverCmdPattern`. Anything else — a build, a test run, a download — is real work and the shell state stands, as does "no jobs found", since no `/proc` must not read as "the turn ended". Ported from my-mux's `demoteServerShells`; keep the two pattern lists in step.
- The format is private and unversioned. Every field decodes as optional; a renamed field must degrade to "no badge", never an error.

Because `Session.ActiveCommand` only reflects the *active pane of the active window*, this state file also covers Claude running in a background window. `tmux.SessionAITool` prefers it over `ActiveCommand` so the badge, token loading, and `mux status` all catch that case — and it hardcodes `"claude"` for the stateful path, since a second state provider would first have to carry its own tool name onto `Session`.

## Claude cost tracking chain

`tmux/claude.go`: pane PID → `pgrep -P` children → `~/.claude/sessions/<childPID>.json` → `{sessionId, cwd}` → `~/.claude/projects/<cwd with "/" replaced by "-">/<sessionId>.jsonl` → sum `usage` fields on `type: "assistant"` lines. Per-1M pricing constants are hardcoded in `estimateCost` (currently Opus-tier rates) and are an estimate only. `ui/app.go` gates the per-tick load on `tool.Name == "claude"` — any other AI CLI would scan for a file that is not there.
