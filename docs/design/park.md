# 보관(`p`)과 보관함(`P`) — 닫았다가 같은 대화로 되살리기

> 관련 코드: tmux/park.go, ui/park.go, ui/app.go(키·행·`NewModel`), cmd/mux/main.go(`UnparkRequest` 분기),
> e2e/park_e2e_test.go, scripts/tmux-assumptions.sh 17절
>
> 2026-11-03 에 들어왔다. 사용자가 정한 것: 키는 `p`/`P`, 되살리는 것은 Claude 대화뿐, 복원하면 바로 그
> 세션으로 간다, 보관된 것이 있으면 목록 맨 위에 행 하나.

## 왜 있나

`x` 로 닫은 Claude 세션은 돌아갈 길이 없다. `~/.claude/sessions/<pid>.json` 은 프로세스가 살아 있을 때만
있으므로 `--claude` 목록에도 다시 나오지 않는다. 대화로 가는 길은 **대화 id 와 그것을 연 디렉터리** 둘뿐이고,
보관은 세션을 닫기 전에 그 둘을 적어 둔다. 세션 이름·메모(`@mux_note`)·`@project_dir` 도 같이 적는다. 그래야
돌아왔을 때 목록에서 같은 자리, 같은 프로젝트 필터에 선다.

## 되살리는 것은 Claude 대화뿐이다

- **다른 AI CLI 는 보관할 수 없다.** mux 가 대화 id 를 아는 도구는 상태 파일을 쓰는 Claude 하나다.
  Claude 가 아닌 세션 행에서 `p` 는 notice 만 띄우고, 창·pane·액션 행에서는 아무것도 하지 않는다.
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
   남는 것이 없으면 `ErrNothingToPark` 이고 세션은 닫지 않는다.
2. **적고, 다시 읽어 확인한다.** 순서가 거꾸로면, 닫기는 됐는데 쓰기가 실패한 경우 길이 없다.
3. **`kill-session -t =name`.** `=` 로 정확히 일치시킨다. 그 이름이 이미 없으면 tmux 는 접두사로 찾아
   `name-2` 를 죽인다(2026-11-03 3.8 실측, 17절). 복원은 이름이 겹치면 바로 그 `name-2` 를 만드는
   기능이라, 같은 이름으로 시작하는 세션이 늘 옆에 있을 수 있다. `x` 의 `KillSession` 은 건드리지 않았다.
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
go test ./tmux -run 'Park|Restore|Forget' && go test ./ui -run 'Park|Popup|Golden'
scripts/tmux-assumptions.sh                         # 17절: =name 지목
go test -tags e2e -run TestParkAndRestore ./e2e/    # 가짜 Claude 로 p y → P enter
```

e2e 는 서버보다 먼저 가짜 Claude 를 `claude` 로 PATH 앞에 둔다(`e2e_test.go` 의 `run`). pane 은 서버를 띄운
때의 환경을 쓰므로, 테스트 중에 PATH 를 바꿔서는 복원이 띄우는 `claude` 에 닿지 않는다.
