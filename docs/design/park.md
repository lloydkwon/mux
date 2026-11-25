# 보관(`p`)과 보관함(`P`) — 닫았다가 같은 대화로 되살리기

> 관련 코드: tmux/park.go, tmux/park_opencode.go, ui/park.go, ui/app.go(키·행·`NewModel`),
> cmd/mux/main.go(`UnparkRequest` 분기), e2e/park_e2e_test.go, e2e/fakeopencode, scripts/tmux-assumptions.sh 17절
>
> 2026-11-03 에 들어왔다. 사용자가 정한 것: 키는 `p`/`P`, 되살리는 것은 Claude 대화뿐, 복원하면 바로 그
> 세션으로 간다, 보관된 것이 있으면 목록 맨 위에 행 하나. 2026-11-11 사용자 요청으로 opencode 대화도
> 되살린다(아래 «opencode» 절).

## 왜 있나

`x` 로 닫은 Claude 세션은 돌아갈 길이 없다. `~/.claude/sessions/<pid>.json` 은 프로세스가 살아 있을 때만
있으므로 `--claude` 목록에도 다시 나오지 않는다. 대화로 가는 길은 **대화 id 와 그것을 연 디렉터리** 둘뿐이고,
보관은 세션을 닫기 전에 그 둘을 적어 둔다. 세션 이름·메모(`@mux_note`)·`@project_dir` 도 같이 적는다. 그래야
돌아왔을 때 목록에서 같은 자리, 같은 프로젝트 필터에 선다.

## 되살리는 것은 Claude 와 opencode 대화다

- **다른 AI CLI 는 보관할 수 없다.** mux 가 대화 id 를 알아낼 수 있는 도구는 상태 파일을 쓰는 Claude 와,
  대화 목록을 API 로 내주는 opencode 둘이다(`tmux.ParkableTool`). 그 밖의 세션 행에서 `p` 는 notice 만
  띄우고, 창·pane·액션 행에서는 아무것도 하지 않는다.
- **한 세션에 둘이 섞여 있으면 둘 다 들어간다.** 대화마다 `tool` 을 적는다. 빈 값은 claude 다 — 이 필드가
  생기기 전의 보관함이 그대로 읽힌다.
- **같은 세션의 셸·서버 pane 은 돌아오지 않는다.** 확인 줄이 «closes the session» 이라고 말하는 이유다.
  창·pane 배치까지 되살리는 것은 세션 하나짜리 tmux-resurrect 이고, 그 범위는 사용자가 고르지 않았다.
- **대화가 여럿이면 창을 하나씩 연다.** 순서는 `TmuxRef` 의 창·pane id 순서다. id 는 만든 순서라 보관할
  때마다 같다. `AISince` 순이면 상태가 바뀔 때마다 순서가 달라진다.
- **exec/plan 짝([pair.md](pair.md))이 그 경우다.** `-exec` 창에 plan 쪽 pane 이 옮겨져 있으면, exec 를
  보관할 때 두 대화가 함께 들어가고 복원하면 창 둘로 돌아온다. `-plan` 세션(패널만 남은 것)은 그대로 남는다.

## 저장: 별도 파일, 읽고-고치고-쓰기

`~/.config/mux/parked.json` 이다(`os.UserConfigDir()`). `preferences.json` 에 넣지 않는다. TUI 는 여럿 열릴 수
있고, 각 TUI 는 시작할 때 읽은 prefs 를 정렬·순서를 바꿀 때마다 통째로 다시 쓴다. 같은 파일에 두면 다른
TUI 가 방금 보관한 항목을 덮어 지운다. 그래서 보관함은 메모리의 사본을 쓰지 않는다. 쓸 때마다 디스크에서
읽고, 고치고, 원자적으로 쓴다(temp → 0600 → rename). `Model.parked` 는 그리기 위한 사본일 뿐이다.

**깨진 파일은 빈 목록이 아니라 오류다.** 빈 목록으로 읽고 다음에 쓰면 그 안의 항목이 전부 사라진다.
보관은 그 쓰기 뒤에 세션을 닫으므로, 대화로 가는 길을 한꺼번에 잃는다. `ParkSession` 은 읽기가 실패하면
세션을 닫기 전에 멈춘다(`TestParkSessionLeavesTheSessionOpenWhenTheListIsCorrupt`).

잠금은 없다. 사람이 누르는 속도라 경합은 받아들이고, 쓴 뒤 다시 읽어 항목이 있는지 확인한다. 없으면
세션을 닫지 않는다. 남는 위험은 TUI 둘이 수십 ms 안에 함께 보관해 한쪽 항목이 덮이는 경우다.

**항목의 열쇠(`ID`)는 보관할 때마다 새로 만든다**(`newParkedID`: 시각 + 첫 대화 id 앞머리). 처음에는 첫 대화의
id 를 그대로 썼고, 코드 리뷰가 그 구멍을 찾았다. `[c1, c2]` 중 c2 만 복원에 실패해 `{ID:c1, [c2]}` 가 남은 뒤
c1 의 세션을 다시 보관하면 `{ID:c1, [c1]}` 이 하나 더 생긴다. 그 상태에서 지우기 한 번이면 둘 다 사라지고,
c2 로 가는 길도 함께 없어진다(`TestPartialRestoreThenReparkKeepsTheOtherConversation`). 읽을 때 열쇠가 겹치면
최신 것만 남긴다.

**같은 대화를 다시 보관하면 그 대화만 옮겨 간다**(`stripConversations`). 옛 항목의 나머지 대화는 남는다. 항목을
통째로 버리면, 손으로 하나만 이어 쓰던 대화의 짝까지 잃는다.

## 보관의 순서

1. **기록 파일이 있는 대화만 고른다**(`transcriptPath`). 갓 띄우고 아직 아무것도 시키지 않은 Claude 는
   `--resume` 할 것이 없다. 되살리면 pane 이 곧바로 끝나고, 그것이 세션의 유일한 pane 이면 세션째 사라진다.
   opencode 는 대화를 아직 안 연 TUI(첫 화면)가 같은 경우다. 남는 것이 없으면 `ErrNothingToPark` 이고
   세션은 닫지 않는다. opencode 의 대화를 정할 수 없으면(아래) 그것도 오류이고 닫지 않는다.
2. **적고, 다시 읽어 확인한다.** 순서가 거꾸로면, 닫기는 됐는데 쓰기가 실패한 경우 길이 없다.
3. **`kill-session -t =name`.** `=` 로 정확히 일치시킨다. 그 이름이 이미 없으면 tmux 는 접두사로 찾아
   `name-2` 를 죽인다(2026-11-03 3.8 실측, 17절). 복원은 이름이 겹치면 바로 그 `name-2` 를 만드는
   기능이라, 같은 이름으로 시작하는 세션이 늘 옆에 있을 수 있다. `x` 의 `KillSession` 은 그때 건드리지 않았다가 2026-11-25 에
   같은 모양(`exactSession`)으로 고쳤다 — 짧은 작업 세션([task.md](task.md))에서 `t-m1` 과 `t-m12` 가 흔한 짝이다.
   kill 이 실패했는데 세션이 아직 있으면 **처음 읽은 보관함을 그대로** 다시 쓴다. 새 항목만 빼면 2단계에서 옛
   항목에서 옮겨 온 대화가 돌아오지 않는다. 이미 닫혔다면 항목이 유일한 길이니 남긴다.
4. **보관한 PID 들이 끝나기를 3초까지 기다린다.** SIGHUP 을 받은 Claude 가 아직 끝나는 중일 때 바로
   복원하면, 그 프로세스가 «tmux 밖에서 살아 있는 사본» 으로 보인다. 그 상태가 busy 면 포크되고, 아니면
   이미 죽은 세션 이름으로 간다. 그 뒤 `ResetClaudeRunCache()` 를 부른다(1초 TTL).

작업 중(Working/Approval/Shell)인 세션도 보관할 수 있다. `x` 도 그렇다. 확인 줄이
«Claude is busy, this turn will be cut» 을 덧붙인다. 대화는 돌아와도 끊긴 턴은 돌아오지 않는다.

**`prefs.Orders[name]` 은 지우지 않는다.** kill 과 다른 점이다. 같은 이름으로 돌아오면 순서 칸(`#3`)도 같이
돌아온다. 보관함에서 지울 때도 정리하지 않는다. tmux 에서 직접 지운 세션의 순서도 이미 남고 있어 해가
없고, 팝업에서 prefs 를 쓰면 별도 파일을 고른 이유(다른 TUI 의 prefs 덮어쓰기)를 다시 끌어들인다.

## opencode

**opencode 는 «어느 pane 이 어느 대화를 열고 있는지» 를 어디에도 적지 않는다.** 2.0.29 에서 잰 것: 상태
디렉터리(`~/.local/state/opencode`)의 파일, 로그(TUI 는 자기 대화를 적지 않는다), 서버 API 118개 경로 어디에도
없다. `session.view` 는 «끝난 턴을 보았다» 만 적고, `session/active` 는 지금 도는 대화만 준다. TUI 하나가 탭으로
여러 대화를 오갈 수도 있다. 그래서 mux 가 추론한다(`opencodePaneConversations`).

1. **TUI 가 뜬 뒤 그 폴더에서 갱신된 최상위 대화 전부**(최신 먼저). 사람이 친 대화는 여기서 잡힌다. 시작
   시각은 `/proc/<pid>/stat` 의 틱에 `btime`(초 단위)을 더한 것이라 1초까지 어긋나므로 1초 여유를 둔다.
   보관된(archived) 대화와 하위 대화(parentID)는 뺀다.
2. 없으면 **`-s <id>` 로 연 대화.** 보관함에서 되살린 뒤 아직 아무것도 안 친 opencode 가 이 경우다. 그 대화가
   지워졌으면(404) 3으로 간다.
3. 없으면 **`-c` 가 이은 대화**, 곧 그 폴더의 가장 최근 대화. 1이 비었으니 TUI 가 뜬 뒤 바뀐 대화가 없다 —
   그 최근 대화가 `-c` 가 이은 것이다.
4. 그래도 없으면 첫 화면이다 — 되살릴 것이 없다.

**1단계는 하나를 고르지 않고 전부 적는다.** 처음에는 가장 최근 것 하나를 골랐고, 코드 리뷰가 그 구멍을 찾았다:
«갱신됐다» 는 누가 갱신했는지 말하지 않는다. 같은 폴더의 TUI B 가 대화를 갱신하고 닫힌 뒤 TUI A 를 보관하면,
B 의 대화가 A 의 것으로 적히고 A 의 대화는 어디에도 적히지 않았다. 둘 이상이면 거부하는 길도 있었지만, 그러면
탭으로 대화 둘을 쓴 TUI 는 영영 보관할 수 없다. 전부 적으면 잃는 대화가 없고, 남의 대화가 섞여도 되살릴 때
창이 하나 더 열릴 뿐이다(대화마다 창 하나, 순서는 최신 먼저).

**이미 보관함에 든 대화는 어느 단계에서도 고르지 않는다**(`parkedIDs`). 같은 폴더의 다른 세션을 보관하며 남긴
대화도 «뜬 뒤 갱신됨» 이다. 그것을 이 세션 항목으로 옮기면 `stripConversations` 가 그쪽 항목에서 빼 가고, 대화가
하나뿐이던 항목은 이름·메모째 사라진다(`TestOpencodeConversationAlreadyParkedStaysInItsEntry`). Claude 는 같은
대화를 다시 보관하면 새 항목으로 옮기는데(다시 보관한 그 세션이 그 대화를 열고 있다는 것을 상태 파일로 안다),
opencode 는 그것을 모르므로 옮기지 않는다. 손으로 탭에서 보관된 대화를 열어 쳤다면 그 대화는 원래 항목에 남는다.

**같은 폴더에 다른 opencode 클라이언트가 살아 있으면 고르지 않는다.** TUI 둘, 또는 TUI 와 `run`·`mini`·`acp` 가
한 폴더에서 돌면 그 폴더의 새 대화가 지금도 섞인다. 1단계에 후보가 있을 때만 따진다 — 아무것도 갱신되지 않은
첫 화면 opencode 가 같은 세션의 Claude 대화 보관까지 막지는 않는다. 보관은 다른 쪽의 pid 를 말하며 멈추고
세션을 닫지 않는다. 백그라운드 서비스(`serve --service`), `--standalone` 이 띄운 자식 서버(`serve`), 관리
명령(`api`, `session` 등)은 대화를 들고 있지 않아 세지 않는다.

**원격 서버(`--server <URL>`)에 붙은 opencode 는 보지 않는다.** 그 대화는 이 기계의 서비스 목록에 없고, 같은 폴더의
로컬 대화를 고르면 틀린 대화다. 그런 TUI 만 있는 세션은 «되살릴 것 없음» 이다.

**폴더는 프로세스의 cwd 다.** opencode 는 폴더 인자(`opencode <dir>`)를 받으면 그리로 chdir 한다(2.0.29·2.2.7
실측, `PWD` 는 그대로 남는다). 그래서 위치 인자를 풀지 않는다. 명령줄은 첫 위치 인자로 하위 명령을 가리고, 그
뒤의 플래그도 읽는다(`opencode . -s <id>`).

**pane 은 프로세스의 조상으로 찾는다.** pane 의 첫 프로세스가 opencode 의 조상 사슬에 있으면 그 pane 이다 —
pane 명령으로 띄웠든(실제 사용 방식: `pane_start_command` 가 `~/.opencode/bin/opencode`), 셸에서 띄웠든, npm
래퍼 밑이든. 프로세스 이름(argv[0] 의 끝)이 정확히 `opencode` 인 것만 본다. `opencodex-desktop` 같은 이웃은 빠진다.
opencode 가 하나라도 돌 때만 `list-panes` 를 부르고, 그 호출이 실패하면 오류다 — 삼키면 섞인 세션에서 Claude
대화만 적고 세션을 닫는다.

**대화 목록은 SQLite 를 읽지 않고 opencode 의 API 에 묻는다** — `opencode api session.list --param directory=…
--param parentID=null --param order=desc --param limit=50`(갱신순, 60ms 안팎, 응답 모양은 2.0.29·2.2.7 같음).
스키마(`session_v2`)는 버전마다 바뀌고, Go 에서 읽으려면 드라이버가 든다. `opencode session list` 는 «현재
프로젝트» 를 cwd 로 정해 폴더를 지목할 수 없다(다른 폴더에서 빈 목록을 돌려줬다). 묻는 실행 파일은 그 TUI 의
것이다 — 같은 버전, 같은 DB 다. 답의 폴더(`location.directory`)는 다시 맞추고, 비었으면 다른 폴더로 본다 —
다음 버전에서 필드 이름이 바뀌고 서버가 `directory` 를 무시하게 되면, 너그러운 비교는 다른 폴더의 대화를 고른다.

**opencode 호출은 10초에서 끊는다**(`outputWithin`). 서비스가 멎으면 호출이 끝나지 않는데, 보관 Cmd 가 돌아오지
않으면 `parking` 이 비지 않아 TUI 의 `p` 가 전부 «still parking» 으로 막히고, 복원은 TUI 가 닫힌 뒤라 아무것도
보이지 않은 채 멈춘다. 제한 시간은 진짜 러너만 갖는다(`timedRunner`) — 목 러너는 `Output` 으로 물러선다.

**실행 파일 경로를 적어 둔다**(`command`). 설치 스크립트는 `~/.opencode/bin` 에 두는데 tmux 서버의 PATH 에는
그 폴더가 없을 수 있다. 절대 경로로 띄웠으면 그 경로(심링크라도 업그레이드를 견딘다), 아니면 `/proc/<pid>/exe`
(업그레이드로 지워졌으면 « (deleted)» 를 뗀다). 되살릴 때 그 경로가 사라졌으면(버전마다 폴더를 따로 쓰는 설치의
업그레이드) mux 의 PATH 에서 찾은 절대 경로로 띄운다.

**`tool` 이 빈 `ses_…` 대화는 opencode 다**(`normalizeParked`). 이 필드를 모르는 옛 mux 가 보관함을 다시 쓰면
필드가 지워지고, 그 대화는 `claude --resume` 으로 가 기록이 없다는 이유로 영영 남기만 한다. opencode 의 id 는
`ses_` 로 시작하고 Claude 의 것은 UUID 라 겹치지 않는다.

**복원은 `<command> -s <id>` 다.** 띄우기 전에 `api session.get` 으로 대화가 아직 있는지 묻는다. opencode 는
없는 id 의 `-s` 를 «새 대화» 로 받으므로, 확인 없이 띄우면 빈 대화가 «되살림» 으로 세어져 항목이 지워진다.
없는 대화의 답은 종료 코드 1 에, stdout 의 `SessionNotFoundError` 본문과 stderr 의 «HTTP 404 Not Found» 다
(2.2.7 실측, 서비스·`--standalone` 모두). 종료 코드만 보면 서비스가 죽은 것과 같아 보이므로 본문의 `_tag` 로
먼저 가린다 — 없으면 «없음»(복원은 항목을 남기고 그렇게 말하고, 보관은 그 `-s` 를 건너뛴다), 물을 수 없으면
오류다. 처음에 «종료 코드 0» 으로 적었던 것은 `| head` 의 종료 코드를 잰 실수였다. `session.get` 은 묻는 폴더와
상관없이 답한다(2.2.7 실측: 다른 프로젝트의 대화를 `/tmp` 에서 물어도 같은 답) — 팝업을 어디서 열었든 된다.
Claude 와 달리 **이미 열린 사본을 찾지 않는다.** opencode 의 TUI 는 서버의 클라이언트라 한 대화에 둘이 붙어도
된다(한 기록 파일에 두 프로세스가 쓰는 Claude 와 다르다). 어느 TUI 가 그 대화를 열고 있는지도 위와 같은 이유로 모른다.

**바쁜 opencode 의 확인 줄은 «this turn may be cut» 이다.** TUI 는 서비스의 클라이언트라 닫아도 턴이 서비스에서
이어질 수 있다. 재지 않았다 — 재려면 모델을 부르는 턴이 하나 있어야 한다. 이어진다면 그 갱신은 보관한 대화에
쌓이고, 그 대화는 이미 보관함에 들어 있으니 다른 세션의 보관이 가져가지 않는다(위의 «이미 보관함에 든 대화»).

남는 한계:
- **옛 대화로 옮겨 가 읽기만 했으면** 그 대화는 갱신되지 않아 1단계에 걸리지 않는다. 같은 TUI 가 뜬 뒤 다른
  대화를 갱신했으면 그쪽이, 아니면 `-s`/`-c` 의 대화가 보관되거나 «되살릴 것 없음» 이 된다. 대화 자체는
  opencode 의 목록(탭·`/sessions`)에 남는다.
- **닫힌 다른 클라이언트나 끝난 `opencode run` 이 같은 폴더에서 갱신한 대화도 함께 적힌다.** 되살리면 창이 하나 더
  열린다. 잃는 쪽이 아니라 더하는 쪽으로 틀린다.
- API 의 응답 모양이 바뀌면 opencode 대화를 찾지 못하거나(«되살릴 것 없음») 오류로 멈춘다 — 틀린 대화를 고르는
  쪽이 아니라 닫지 않는 쪽으로 실패하게 짰다. 버전을 올리면 이 절의 실측(`api session.list`·`session.get` 의 모양,
  404 의 종료 코드, chdir)을 다시 잰다. 2.0.29 → 2.2.7 에서 바뀐 것은 없었다(`update` 하위 명령 별칭만 빠졌다).
- `/proc` 이 없는 곳(macOS)에서는 opencode 를 찾지 못해 보관할 수 없다.

## 복원

**세션 생성과 attach 는 TUI 밖에서 한다.** `Update` 는 `unparkTarget` 을 적고 `tea.Quit` 하고,
`cmd/mux` 가 `RestoreParked` → `AttachToSession` 을 부른다. `resumeTarget` 과 같은 이유다([tui.md](tui.md),
[claude-mode.md](claude-mode.md)).

**같은 id 로 `--resume` 한다. 포크하지 않는다.** 근거는 [claude-mode.md](claude-mode.md) 와 같다. 같은 id
라야 다음 스캔에서 한 대화가 한 행이고, 포크한 자식은 부모를 가리키는 필드가 없다.

**이미 살아 있는 대화는 띄우지 않는다.** 보관한 뒤 사용자가 손으로 `claude --resume` 했을 수 있다.
`RestoreParked` 는 캐시를 비우고 `ClaudeRuns()` 와 대조한다. 판정은 이렇다.
- 보관 때의 PID 와 같은 run 은 무시한다. 위 4단계의, 끝나는 중인 그 프로세스다.
- tmux 에 살아 있으면 그 대화는 띄우지 않는다. 띄울 것이 하나도 없으면 첫 대화가 사는 세션으로 간다.
- tmux 밖에서 **작업 중**이면 `ClaudeBusyElsewhere` 로 포크한다. `--claude` 의 enter 와 같은 규칙이다.

**띄우기 전에 기록 파일을 다시 확인한다.** Claude 는 오래된 기록을 스스로 지운다(`cleanupPeriodDays`).
기록 없이 `--resume` 하면 pane 이 곧바로 끝나는데, 그것을 «띄움» 으로 세면 항목까지 지워진다.

**대상은 전부 `=name:` 이다.** `new-window` 와 `set-option`(메모·`@project_dir`)이 그렇다. `set-option -t` 는
pane 대상이라 `=name` 만으로는 «no such session» 이다. 콜론 없는 맨 이름은 그 세션이 없을 때 접두사로
`name-2` 에 쓴다(17절, 3.4·3.8). 메모는 `SetSessionNote` 를 거치지 않고 같은 `sanitizeNote` 를 쓴다.

**`-c` 의 디렉터리는 `#` 를 `##` 로 넘긴다**(`tmuxDirArg`). tmux 는 `-c` 를 포맷으로 펼치고, `#(…)` 는 셸 명령으로
실행한다. 2026-11-03 격리 서버에서 `c#(touch …)d` 라는 디렉터리를 `-c` 로 넘기자 그 명령이 실제로 돌았다.
디렉터리 이름은 클론한 저장소가 정할 수 있다. 같은 구멍이 `--claude` 입양(`CreateSessionWithCommand`),
`n` 의 디렉터리 지정(`CreateSessionWithDir`), 패널의 `split-window -c` 에도 있었고 셋 다 같이 막았다.

**띄우지 못한 대화만 항목에 남는다.** cwd 디렉터리가 사라진 경우(지운 worktree), 기록이 지워진 경우, 창
생성이 실패한 경우다.
TUI 는 이미 닫혔고 팝업도 이 프로세스와 함께 닫혀서 경고를 보여 줄 화면이 없다. 그래서 다음에 Parked 행으로
다시 보이는 남은 항목이 실패의 보고다. 아무것도 띄우지 못하면 항목은 그대로이고, 부트스트랩 팝업이면
`DetachClient` 를 먼저 부른다(`q` 로 닫을 때와 같다).

남는 위험: 기록이 있는데도 띄운 pane 이 곧 죽는 경우는 «띄움» 으로 세어 항목이 지워진다. 그래도 기록은 디스크에
남으므로 `claude --resume` 의 목록에서 여전히 찾을 수 있다.

## TUI

- **`p`** 는 `writesBlocked()`(소스가 설정됨)면 꺼진다. 확인 줄은 kill 확인과 같은 자리다.
- **`y` 는 두 단계다.** 먼저 `parkStartedMsg` 로 확인 줄을 닫고 "parking…" 을 띄운 다음, **그 뒤의 Cmd 안에서만**
  `parkSession` 을 부른다. 보관은 PID 대기로 몇 초 걸린다. 확인 줄이 그동안 떠 있으면 사이에 누른 키가 «취소»
  로 먹히고, `y` 를 한 번 더 누르면 kill 뒤의 두 번째 보관이 «nothing was closed» 로 거꾸로 보고한다. 그래서
  `parking` 이 그 세션을 적어 두고 끝날 때까지 `p` 를 받지 않는다. 늦게 온 결과는 확인 줄일 때만 모드를 바꾼다.
- ui 에는 목 러너가 없어서, 시임(`parkSession`, `loadParked`, `forgetParked`)을 바꿔 끼워 위의 것을 확인한다
  (`TestConfirmParkRunsOnlyInTheCmd`).
- **Parked 행**(`itemParked`, `≡`)은 액션 행이 보이는 조건(쓰기 가능, 필터 없음 또는 시딩 필터)에서 보관된
  것이 있을 때만 생긴다. enter·더블클릭 모두 `activateCurrent` 를 거친다. 그 switch 에서 빠지면 더블클릭이
  default 의 빈 attach 로 빠져 mux 가 그냥 닫힌다(`TestDoubleClickOnTheParkedRowOpensThePopup`).
- **보관함은 `NewModel` 에서 읽는다.** 첫 세션 목록보다 늦게 오는 Cmd 로 읽으면 Parked 행이 그 사이에
  끼어든다. 그러면 `firstSessionIndex` 로 놓인 커서가 Parked 행을 가리키고, 첫 enter 가 attach 가 아니라
  팝업이 된다. 그 뒤로는 보관·지우기·팝업 열기 때만 다시 읽고 틱마다 읽지 않는다. 행이 생기고 사라질 때는
  커서를 인덱스가 아니라 같은 세션에 다시 앉힌다(`setParked`).
- **`notice`** 는 다음 키까지 extraBar 에 남는 한 줄이다. `m.err` 는 다음 틱의 `sessionsLoadedMsg` 가
  덮어써 500ms 만에 사라진다. 순서는 확인 줄 바로 아래, 필터보다 위다. VS Code 에서 연 목록은 시딩 필터가
  늘 서 있어서, 필터 아래에 두면 영영 보이지 않는다.
- **팝업**은 `viewWithOverlay` 상자다. 그 함수는 크기 상한이 없어서 줄마다 폭을 맞추고 행 수를 화면 높이로
  자른다. 넘치면 `listOffset` 로 스크롤한다(`TestPopupFitsTheScreen`). `d` 는 줄 안에서 한 번 묻는다.
- **지금 보고 있는 세션을 보관하면** `x` 처럼 그 세션이 닫히면서 팝업째 사라진다.

## 확인

```bash
go test ./tmux -run 'Park|Restore|Forget|Opencode' && go test ./ui -run 'Park|Popup|Golden'
scripts/tmux-assumptions.sh                         # 17절: =name 지목
go test -tags e2e -run TestParkAndRestore ./e2e/    # 가짜 Claude·가짜 opencode 로 p y → P enter
```

가짜 opencode(`e2e/fakeopencode`)는 이름이 `opencode` 인 실행 파일로 빌드한다 — mux 는 `/proc` 에서 그 이름으로
알아본다. 대화는 `$HOME/.fakeopencode/<id>.json` 이고, `api session.list`·`session.get` 의 답(404 의 종료 코드 1
포함)과 `-s` 를 진짜 2.0.29·2.2.7 의 모양대로 흉내 낸다. 진짜 opencode 로는 2026-11-11 격리 서버(`-L`)에서 기존 대화를 `-s` 로 연 TUI 를
보관하고 되살려 확인했다(새 대화를 만들지 않는 방법).

e2e 는 서버보다 먼저 가짜 Claude 를 `claude` 로 PATH 앞에 둔다(`e2e_test.go` 의 `run`). pane 은 서버를 띄운
때의 환경을 쓰므로, 테스트 중에 PATH 를 바꿔서는 복원이 띄우는 `claude` 에 닿지 않는다.
