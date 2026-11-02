# `mux agent` — 에이전트 협업 API (`ls` · `prompt` · `wait` · `read`)

> 관련 코드: tmux/agent.go, tmux/agent_prompt.go, tmux/agent_wait.go, tmux/transcript.go,
> cmd/mux/agent.go, e2e/fakeclaude, e2e/agent_e2e_test.go
>
> 2026-11-02 에 만들었다. 실측 값은 모두 Claude Code 2.1.312 · tmux 3.4 의 것이다.

## 무엇이고 무엇이 아닌가

한 tmux pane 의 Claude 가 다른 pane 의 Claude 에게 **일을 보내고(prompt), 끝나기를
기다리고(wait), 회신을 읽는다(read)**. 지금까지 이것을 해 주는 것은 herdr 뿐이었고
(`herdr agent prompt/wait/read`), 그래서 검토↔실행 회차 루프(`~/.claude/herdr-loop`)는
herdr 에서만 돌았다.

```sh
T=$(mux agent prompt govgen-plan "/govgen-reply ~/.claude/govgen/rounds/G-93/message.md")
mux agent wait govgen-plan --after "$T" --timeout 1h
mux agent read govgen-plan --after "$T"
```

**하지 않는 것**(2026-11-02 사용자 결정, «기능은 적게, 각각은 확실하게»): 여러 기계 묶기,
`start`(Claude 띄우기 — `tmux new -d -s plan 'claude -n plan'` 이면 된다), 패널·이벤트 기록
변경, 회차 루프 이관(아래 대응표만 둔다). **Claude 만** 다룬다: prompt 는 Claude 의 `❯`
입력창에, wait 는 상태 파일에, read 는 대화 기록에 기대는데 codex 등에는 셋 다 없다.

## 대상 지목 — 우선순위가 없다

`<대상>` 은 pane id(`%12`), Claude 가 하나뿐인 tmux 세션 이름, 또는 Claude 대화 이름
(`claude -n`·`/rename`·자동 이름 — 상태 파일의 `name`)이다. 세션과 이름이 둘 다 맞거나 후보가
여럿이면 **후보를 pane id 와 함께 늘어놓는 오류**다(`ResolveAgent`). 조용히 고르는 규칙은 두지
않는다:

- 짝 합치기(pair.md) 뒤에는 plan Claude 가 exec 세션의 창에 산다 — exec 세션에 Claude 가 둘,
  plan 세션에 0 이다.
- 같은 이름의 대화가 여러 프로젝트에 있는 것이 보통이다(`claude_sessions.go` 의 `uniqueRunNames` 주석).

어느 규칙으로 골라도 언젠가 엉뚱한 Claude 에게 보낸다. 오류는 고칠 수 있고, 잘못 보낸 프롬프트는
되돌릴 수 없다.

목록(`Agents`)은 **캐시 없이** 상태 파일을 읽는다. `ClaudeRuns`(대화 단위로 합치고 1초 TTL)와
`ClaudeStatuses`(세션마다 하나만 남김)를 쓰지 않는 이유다 — pane 이 사라지거나 1초 묵은 상태로
보낸다. Claude 가 적은 tmux 위치는 믿지 않는다: 다른 서버의 pane 을 가리키는 살아 있는 상태
파일이 흔해서(`water-quality:@4.%6` 와 `my-mux:@4.%6`), `claudeSessionName` 이 지금 서버의 pane 과
프로세스 트리로 거른다.

## 상태 단어

`working | blocked | idle | shell | unknown`. `AIState.String()` 을 쓰지 않는다 — 그것은
`status --json` 의 오래된 약속대로 Ready 를 `"waiting"` 으로 내서, «끝났다»와 «승인 대기»가
섞인다. `shell` 은 개발 서버만 남은 경우 `idle` 로 내린다(`demoteServerShell`, 목록·패널과 같다).

## prompt — 글은 한 번만 치고, Enter 는 확인한 뒤에만

순서(`PromptAgent`):

1. **글 검사.** 한 줄, 700자 이하, `!` 로 시작하지 않음, 제어 문자·결합 문자 없음.
2. **pane 별 `flock`.** 두 mux 가 한 pane 에 동시에 치면 글자가 섞인다. 10초 넘게 못 잡으면 5.
3. **상태.** blocked 이면 3 — `--force` 로도 보내지 않는다(보내면 대화상자에 답하는 셈이다).
   working·shell·unknown 이면 5, `--force` 면 보낸다(Claude 가 대기열에 넣는다).
4. **pane.** 복사 모드면 5(`--force` 면 `-X cancel`), `#{pane_synchronized}` 면 늘 5.
5. **입력창을 읽는다.** 못 찾으면(대화상자·메뉴) 5. 이것이 `before` 다.
6. **한 번 친다** — `send-keys -t %N -l -- <글>`.
7. **0.6초 뒤부터 2초 안에 입력창이 정확히 우리 글이 되어야** Enter 를 누른다. 아니면 되돌린다:
   지우기 직전에 다시 읽어 입력창이 «`before` + 우리 글» 이거나 «우리 글» 일 때만 친 글자 수만큼
   `BSpace` 를 누른다. 돌아왔으면 5. 그 사이 사람이 이어 쳤다면 BSpace 가 사람 글부터 지우므로
   **건드리지 않고** 1.
8. **Enter 는 따로**, 많아야 두 번. 제출 확인(5초, Enter 를 누른 시각 기준): 입력창이 비었다 /
   대상이 idle 이었는데 입력창이 사라졌고(대화상자) 상태가 Enter 뒤 바뀌었다 / 대화 기록에 Enter
   뒤의 사람 턴이 있다. 입력창에 아직 우리 글만 있으면 Enter 가 먹힌 것이라 한 번 더 누른다 — 두 번
   제출될 수 없다(Enter 는 입력창에 있는 것만 제출하고, 제출되면 입력창이 빈다). 다시 누르기 전에
   상태가 Enter 뒤 바뀌었으면 누르지 않고 1 이다 — Claude 가 잠시 멈춰 화면이 늦게 따라온 것이면 첫
   Enter 는 이미 제출됐고 두 번째는 빈 입력창(제안 문구)에 떨어진다. 두 번 다 먹히면 되돌리고 6.
9. 성공하면 **stdout 에 토큰 하나만** 낸다.

왜 이렇게 하는가:

- **`-l --`.** `-l` 없이는 «Space»·«Up» 같은 낱말이 키 이름이 되고, `--` 없이는 `-n` 으로 시작하는
  글이 플래그가 된다(tmux-assumptions 15절).
- **끝의 `;` 은 `\;` 로.** tmux 는 «;» 로 끝나는 인자를 명령 구분자로 읽는다 — 뒤 인자가 새 명령이
  된다(3.4 실측, `tmuxLiteral`).
- **Enter 는 0.6초 뒤 따로.** 글과 붙여 보내면 TUI 가 Enter 를 삼킨다 — 회차 루프가 먼저 재서 정했다.
- **빈 입력창을 사전 조건으로 두지 않는다.** Claude 는 턴 뒤 빈 입력창에 흐린 제안 문구를 띄울 때가
  있고(대화 기록의 `promptSource: suggestion_accepted`), 글자 캡처로는 사람의 초안과 구별되지 않는다.
  대신 «친 뒤 입력창이 정확히 우리 글인가» 를 본다 — 제안 문구는 치기 시작하면 사라지고, 초안은
  남아서 어긋난다. 그러면 친 만큼만 지운다. **빈 입력창에서는 Enter 를 누르지 않는다** — 제안 문구가
  제출될 수 있다. (2026-11-02 의 실측에서는 제안 문구가 나타나지 않아 그리는 방식을 재지 못했다.
  나타나도 실패는 안전한 쪽 — 어긋남 → 되돌림 → 5 — 이다.)
- **700자.** 실측: 한 번에 들어온 글은 800자까지 입력창에 펼쳐지고 801자부터 `[Pasted text #n]`
  으로 접힌다. 바이트가 아니라 글자로 센다(한글도 같다). 접히면 정확 일치 확인이 깨지고 되돌릴
  BSpace 수도 맞지 않는다. 긴 본문은 파일에 쓰고 한 줄짜리 경로를 보낸다 — 회차 루프가 이미 그렇다.
- **화면에서 합쳐지는 글자열을 받지 않는다.** 되돌리기는 친 «글자(rune) 수» 만큼 BSpace 다. ZWJ·피부색·
  국기 이모지, 결합 부호, NFD 로 분해된 한글 자모(유니코드 분류는 글자지만 화면에서 한 음절로 합쳐진다)는
  한 번에 지워져 초안까지 지울 수 있다. 규칙은 «rune 수 = grapheme 수»(`uniseg`)이고, 보이지 않는
  문자(Cf)도 받지 않는다.
- **상태 변화는 idle 이었을 때만 제출의 증거다.** `--force` 로 작업 중에 보냈다면 앞 턴이 끝나거나 앞
  턴의 권한 대화상자가 떠도 상태가 바뀐다 — Enter 가 먹혔는데 성공으로 읽게 된다(리뷰에서 재현).
- **`--force` 는 입력창을 비우지 않는다.** 처음 계획은 `C-u` 로 비우는 것이었는데, 실측하니 `C-u` 는
  줄바꿈된 긴 글의 화면 한 줄만 지웠다. 비우기가 확실하지 않으면 사람의 초안을 지울 위험만 남는다.
- **정확 일치 확인은 «입력창이 입력 버퍼를 그린다» 를 전제한다.** 진짜 Claude 는 버퍼를 스스로 들고
  다시 그린다. e2e 의 가짜 Claude 를 처음에 tty 에코에 맡겼더니, 패널이 붙어 창 크기가 바뀔 때 다시
  그린 화면에서 친 글이 사라지고 버퍼에는 남아 «초안 + 우리 글» 이 제출됐다 — 그래서 가짜도 raw
  모드로 버퍼를 직접 그린다.

입력창 모양(실측): `─` 선(색 244) 바로 아래, 0번 칸의 `❯` + U+00A0, 그다음 `─` 선. 빈 입력창은
반전된 공백 하나(커서)뿐이다. 위쪽 대화 기록과 대기열에도 `❯ 글` 줄이 있지만 그 위는 `─` 가 아니고,
권한 대화상자의 `❯ 1. Yes` 는 들여쓰여 있다(`parseInputBox`). 인자가 붙은 슬래시 명령
(`/rename probe-test`)도 자동완성 메뉴가 끼지 않고 그대로 입력창에 보이고 제출된다.

## 토큰 `<sessionId>@<보낸 시각 ms>`

`wait`·`read --after` 가 «이 프롬프트가 연 턴» 을 찾는 기준이다. 실제 모양은
`<sessionId>@<ms>#<글 해시>` 다. 시각만으로는 안 된다:

- 턴 도중에 compact 가 일어나면 기록이 새 파일로 이어진다(`continued-in`). 프롬프트는 앞 파일에,
  회신은 뒤 파일에 있으므로 토큰의 대화에서 시작해 따라가야 한다.
- 대상이 `/clear` 등으로 다른 대화가 됐으면 지금 대화가 토큰의 대화에서 이어진 것이 아니다 —
  다른 대화를 읽지 않도록 1 로 멈춘다(`checkTokenChain`). compact 직후 다음 파일이 아직 없어도 그
  대화 id 는 사슬에 넣는다 — 상태 파일이 이미 그 id 를 가리킬 수 있다.
- **글 해시**: 작업 중에 보낸 글은 꺼낼 때의 시각으로 기록되므로, 먼저 대기열에 있던 다른 글의
  턴도 «보낸 시각 뒤의 첫 사람 턴» 이다. 그 시각 이후의 사람 턴 중 본문 해시가 같은 것을 먼저
  고른다. 슬래시 명령처럼 본문이 바뀌어 기록되면 맞는 턴이 없고, 그때는 시각으로 고른다.

기록의 시각은 `time.RFC3339Nano` 로 풀어 시간으로 비교한다. 문자열로 비교하면 `…43.512Z` 가
`…43Z` 보다 앞에 정렬된다.

## wait — 상태 파일이 빠른 길, 대화 기록이 판정

0.3초마다 상태 파일 하나만 읽는다(tmux 호출 없음). blocked 면 3, 프로세스가 사라지면 1.
`--after` 가 없으면 idle 이 곧 끝이다. 있으면 idle 일 때 대화 기록을(1초에 한 번까지) 읽어
`waitStep` 이 정한다:

- 프롬프트 턴이 기록에 있고, **idle 이 그 턴이 시작된 뒤에 시작됐거나**, 기록상 턴이 끝났으면
  (다음 턴 시작·`turn_duration`) 끝이다. «마지막 응답 그룹이 end_turn 으로 끝났다» 는 증거로 받지
  않는다 — Stop 훅이 턴을 막아 이어 가면(이 사용자의 go vet 훅) 그 뒤에도 턴이 계속된다.
- 기준이 «보낸 시각» 이 아니라 «기록에 프롬프트 턴이 시작된 시각» 인 이유: 작업 중에 보낸
  (`--force`) 글은 앞 턴이 끝난 뒤에야 턴이 된다(실측: 대기열에서 꺼낼 때 `turnIndex` 를 가진 사람
  턴 줄이 쓰인다). 그 사이 잠깐의 idle 도 보낸 시각보다는 늦다.
- 기록상의 턴 끝도 받는 이유: 백그라운드 셸이 돌면 상태가 `shell` 로 굳어 `statusUpdatedAt` 이
  움직이지 않는다 — 개발 서버만 남아 idle 로 강등되면 «idle 이 턴 뒤에 시작됐다» 가 영영 참이 안 된다.
- idle 인데 프롬프트가 기록에 15초 넘게 없으면 7(기록에 안 보인다). 15초는 idle 이 시작된 때부터도
  센다 — 앞 턴이 길었다면 기록이 따라올 시간을 준다. 턴을 만들지 않는 로컬 명령(`/rename`·`/model`)도
  7 이 된다 — 그런 것은 `--after` 없이 기다린다.
- 상태 파일을 읽다 실패해도(Claude 는 제자리에 덮어써서 반쯤 쓰인 내용이 읽힌다) 프로세스가 살아
  있으면 이전 상태로 다음 폴링을 기다린다. 한 번의 찢어진 읽기로 한 시간짜리 wait 가 끝나면 안 된다.

`--until` 은 두지 않는다. 회차 루프가 겪은 «보내자마자 기다리면 아직 지난 턴의 idle 이라 0.07초 만에
끝난다» 를 토큰이 막는다 — herdr 의 두 단계 대기(`--until working` 다음 `--until idle`)가 필요 없다.

## read — 화면이 아니라 대화 기록

Claude 는 대체 화면에 그려서 capture-pane 으로는 보이는 부분만 읽힌다. 회신은 기록에서 꺼낸다
(`~/.claude/projects/<encodePath(cwd)>/<sessionId>.jsonl`, 없으면 `projects/*/<sessionId>.jsonl`).

- **턴**: `turnPosition` 이 있는 user 줄이 턴을 연다(사람·작업 알림·peer·system 모두). compact 요약
  줄은 같은 번호를 다시 쓰므로 뺀다. 사람 턴은 `origin.kind == "human"`. `--after` 면 그 시각 이후의
  첫 사람 턴, 아니면 마지막 턴. 턴은 번호가 다른 다음 턴에서 끝난다. `turnPosition` 이 하나도 없는
  옛 기록은 보수적인 대체 규칙(글이 있고 meta·tool_result·로컬 명령이 아닌 user 줄)을 쓴다.
- **회신은 그 턴의 마지막 응답 그룹**(`requestId`, 없으면 `message.id`, `uuid`)의 글이고, 글이 있으며
  `stop_reason` 이 `tool_use` 가 아닐 때만 인정한다. **앞 그룹으로 물러서지 않는다** — 물러서면
  «확인해 보겠다» 같은 중간 문장이 회신으로 나간다. 마지막 그룹에 글이 없으면 2(실측: 최근 기록
  494턴 중 470턴이 이 형태로 끝났고, 나머지는 회신 글이 없다 — 회차 루프의 G-67 이 그 경우다).
- **늦게 쓰이는 기록**: idle 이 된 지 30초 안이면 2초 간격으로 8번 다시 읽는다.
- 턴이 아직 진행 중이면 5, 막혀 있으면 3, 기록 파일이 아예 없으면 1.
- 줄은 `bufio.Reader` 로 읽는다 — 1MB 넘는 줄과 NUL 이 든 줄이 실제로 있다. 수 MB 의
  `api-request-blob` 줄은 디코딩하기 전에 거른다(`transcriptNeedles`).

## 종료 코드

| 코드 | 뜻 |
|---|---|
| 0 | 성공 |
| 1 | 오류 — 대상 없음·모호함, 서버 없음, 에이전트 종료, 자기 pane 지목, 토큰 불일치, «제출됐는지 모름»(다시 보내지 말 것) |
| 2 | read: 회신 본문이 없다 |
| 3 | 대상이 대화상자(승인 대기)에 막혔다 |
| 4 | wait: 시간이 다 됐다 |
| 5 | 보낼 수 없는 상태라 **아무것도 보내지 않았다**(작업 중, 입력창에 다른 글, 복사 모드 등) / read: 턴 진행 중 |
| 6 | prompt: 제출되지 않았고 되돌렸다 — **다시 보내도 된다** |
| 7 | wait·read: 프롬프트가 기록에 나타나지 않았다 — 제출은 됐을 수 있으니 **다시 보내지 않는다** |

2·3·4 를 회차 루프 `wait.sh` 의 코드와 맞췄다 — 이관할 때 분기를 그대로 쓰게. 6 과 7 을 떼어 둔
이유: 처음엔 둘 다 6 이었는데, «6 이면 다시 보낸다» 는 스크립트가 wait 의 6 에서 두 번 제출한다.

## herdr → mux 대응표 (회차 루프 이관용)

| herdr | mux |
|---|---|
| `herdr agent list` / `get <이름>` | `mux agent ls [--json]` (Claude 만; idle·done → idle, shell 이 더 있다) |
| `herdr agent prompt <t> <글>` | `T=$(mux agent prompt <t> <글>)` — `agent_blocked` → 3, `agent_prompt_stalled` → 6. herdr 는 작업 중인 대상도 받지만 mux 는 `--force` 가 없으면 5 |
| `prompt --wait` | `mux agent wait <t> --after "$T"` |
| `wait --until working --until blocked --timeout 15000` (1단계) | 필요 없다 — `--after` 가 대신한다 |
| `wait --until idle --until done --until blocked --timeout MS` | `mux agent wait <t> --after "$T" --timeout 1h` → 0/3/4 (기록에 안 보이면 7) |
| `fetch_reply.py` + 2초×8 재시도 | `mux agent read <t> --after "$T"` → 0/2 (스스로 재시도한다) |
| `herdr agent read --source visible` | 없음 (`tmux capture-pane -p -t %id`) |
| herdr 이름 (`agent rename`) | Claude `-n` 이름, 세션 이름, 또는 `%pane` |
| wait.sh 종료 코드 0/2/3/4 | wait 0/3/4 + read 0/2 |

## Claude 를 올린 뒤 다시 잴 것

CI 는 진짜 Claude 를 띄우지 않는다(e2e 는 가짜 Claude). Claude 를 올리면 격리 서버(`tmux -L …`,
Claude 환경 변수를 지운 env)에서 haiku 로 띄워 다시 잰다:

1. 입력창 모양 — `─` 선, `❯` + U+00A0, 빈 입력창의 커서 칸. 바뀌면 `parseInputBox` 와 가짜 Claude 를 고친다.
2. 접힘 경계(800자) — 바뀌면 `maxPromptRunes` 를 고친다.
3. 글을 친 뒤 따로 보낸 Enter 가 제출되는가, 인자 붙은 슬래시 명령이 그대로 제출되는가.
4. 작업 중에 보낸 글이 Enter 즉시 입력창에서 빠지고, 앞 턴 뒤에 `turnPosition` 을 가진 사람 턴으로 기록되는가.
5. 상태 파일의 `status`·`statusUpdatedAt`·`tmux`·`name`, 기록의 `turnPosition`·`origin`·`requestId`·`stop_reason`·`continued-in`.
6. 제안 문구가 나타나면 `capture-pane -e` 로 어떻게 그려지는지, 치기 시작하면 사라지는지.

## 알려진 한계

- **macOS**: `/proc` 이 없어 «이 pane 의 프로세스 트리에 그 Claude 가 있는가» 를 모른다. 다른 tmux
  서버의 같은 `%N` 을 가리키는 상태 파일을 이 서버의 pane 으로 오인할 수 있다(목록·패널도 같은
  한계다). 그 pane 에 Claude 입력창(`─` 아래의 `❯`)이 없으면 prompt 는 5 로 멈춘다.
- **Claude 의 vim 모드**: NORMAL 상태면 친 글이 명령이 된다. 재 보지 못했다 — 쓰는 사람이 생기면 잰다.
- **치는 도중의 Ctrl-C**: 신호를 받아 되돌리지 않는다. 입력창에 글이 남을 수 있다.

## 버린 것

- **bracketed paste / `paste-buffer -p`** — 글이 `[Pasted text]` 로 접힐 수 있어 정확 일치 확인이 깨진다.
- **Claude 의 peer 소켓**(`messagingSocketPath`, `cc-socks`) — 키 입력이 필요 없지만 비공개 프로토콜이고,
  메시지가 peer/meta 턴으로 들어가 `/govgen-reply` 같은 슬래시 명령이 실행되지 않는다.
- **화면 긁기로 회신 읽기** — 대체 화면이라 보이는 부분만 읽힌다.
- **`wait --until`** — 토큰이 대신한다.
- **codex 등 다른 도구** — 입력창·상태 파일·대화 기록 중 어느 것도 없다. `ls --json` 에 `tool` 필드만 남겼다.
- **`turn_duration` 을 필수 신호로** — 턴의 약 75% 에만 있다(실측 478턴 중 359). 상태 파일이 정상이면
  필요 없고, 상태가 굳은 경우(위)에만 다음 턴과 함께 끝의 증거로 쓴다.
- **마지막 응답 그룹의 모양으로 턴 끝 판정** — Stop 훅 때문에(위).
- **`--force` 의 입력창 비우기(`C-u`)** — 위에 적었다.
