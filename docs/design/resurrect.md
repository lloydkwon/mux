# 세션 저장·복원 (`# mux resurrect { }`)

> 관련 코드: tmux/resurrect.go, tmux/setup.go (`InstallIntegration`), ui/setupoffer.go, cmd/mux (`setup-resurrect`)
>
> 유령 패널을 치우는 쪽은 [panel.md](panel.md) 의 «The panel names its own pane» 절이다. 이 문서는 그
> 정리가 기대는 설정을 누가, 왜 써 넣는지다.

**tmux 서버는 세션을 메모리에만 둔다.** 서버가 끝나면 세션도 전부 끝나고 디스크에는 아무것도 남지 않는다.
WSL 에서는 Windows 를 재시작할 때마다 그렇다 — 2026-10-31 에 재시작 뒤 `mux` 를 열었더니 부팅 때 새로 만든
`sys` 하나만 있었고, 작업하던 세션은 mux 의 이벤트 로그(`events.json`)와 Claude 대화 기록으로 이름과
디렉터리를 거꾸로 찾아 손으로 다시 만들어야 했다. 표준 답은 tmux-resurrect(저장·복원)와
tmux-continuum(15분마다 자동 저장, 서버 시작 때 자동 복원)이고, mux 는 이것을 세 번째 소유 영역으로 깐다.

## 왜 mux 가 깔아 주는가

mux 는 이미 resurrect 를 전제로 한 코드를 갖고 있다 — `findGhostPane` 이 복원된 옛 패널을 치운다. 그런데
그 판정은 **pane 내용 저장이 켜져 있을 때만** 성립한다. 복원된 패널 자리의 셸을 알아보는 단서는 둘이다:

- 시작 명령 `cat …/resurrect/restore/pane_contents/…; exec $SHELL` (`resurrectRestoreMark`)
- 다시 그려진 화면 맨 위의 패널 머리글 `AI 세션` (`panelHeader`)

둘 다 `@resurrect-capture-pane-contents on` 이어야 생긴다. 꺼져 있으면 시작 명령은 비고, 셸 프롬프트가
pane 제목(`mux panel`)마저 덮어써서 아무 표시도 남지 않는다. 격리 서버에서 재현했다: 옵션이 꺼져 있으면
복원 뒤 창을 고를 때마다 죽은 셸 옆에 새 패널이 열려 세 칸이 되고, 켜면 죽은 셸이 닫히고 두 칸이 된다.
그래서 사용자가 resurrect 를 직접 쓰더라도 이 한 줄은 mux 가 쓴다.

같은 재현에서 한 번 헛짚은 것: 시험용 `@resurrect-dir` 을 `…/res` 로 두었더니 표시 경로가
`…/res/restore/pane_contents` 가 되어 `resurrectRestoreMark` 와 맞지 않았다. 기본 디렉터리(`…/tmux/resurrect`)
에서는 맞는다. 사용자가 `@resurrect-dir` 을 `resurrect` 로 끝나지 않는 곳으로 바꾸면 같은 일이 생긴다 —
그때 남는 단서는 제목뿐이다.

처음에는 이것으로 충분하다고 보고, 복원 직후 훅(아래)은 «유령 판정과 같은 일을 두 번 한다»며 버렸다.
틀렸다 — 다음 절.

## 복원 직후 훅 (`mux resurrect-hook pre|post`)

2026-11-01 재부팅 뒤 `mux` 창이 세 칸이 됐다: 빈 복원 셸, 패널 폭(46칸)에 끼인 Claude 대화, 새 패널.
원인은 두 가지가 겹친 것이다.

- **resurrect 는 활성 pane 을 복원하지 못한다.** `restore_active_pane_for_each_window` 는
  `switch-client -t 세션:창` 뒤 `select-pane -t <번호>` 로 고른다. continuum 은 서버가 시작될 때,
  아무도 붙기 전에 복원하므로 switch-client 가 실패하고, 번호만 준 select-pane 은 엉뚱한 창을 가리킨다.
  그래서 창마다 마지막에 split 된 pane 이 활성으로 남는데, 패널은 늘 마지막에 split 된다 — **모든 창에서
  죽은 패널이 활성 pane** 이었다. 격리 서버에서 재현했다(저장 때 0번 활성 → 복원 뒤 1번 활성).
- **유령 판정은 자식 프로세스가 있는 pane 을 살려 둔다**(누가 입양한 셸일 수 있으므로). 사용자가 붙자마자
  친 첫 명령(`claude --continue`)이 활성인 죽은 패널로 들어갔고, 그 순간부터 그 pane 은 자식이 있어 영영
  치워지지 않았다.

그래서 mux 가 resurrect 의 `pre-restore-all`/`post-restore-all` 훅으로 복원이 끝나는 순간 두 일을 한다.

1. **pre**: 지금 있는 가장 큰 pane id 를 `@mux_restore_floor` 에 적는다.
2. **post**: 저장 파일(`<dir>/last`)의 활성 pane 을 전체 주소 `=세션:창.pane` 으로 다시 고른 뒤,
   저장 때 패널(`mux panel` 제목 **그리고** `mux` 명령)이던 자리를 닫는다.

순서가 load-bearing 이다. 닫기를 먼저 하면 닫힌 pane 오른쪽의 번호가 당겨져 저장 파일의 번호와 어긋난다.
닫기는 죽은 패널이 활성일 때만 활성 pane 을 옮기고, 그때 tmux 가 이웃에게 넘긴다.

닫는 조건은 셋이고, 자식 프로세스 검사는 **하지 않는다** — 방금 생긴 pane 을 누가 입양했을 리 없기 때문이고,
그걸 증명하는 것이 floor 다.

- id 가 floor 보다 크다 — 이번 복원이 만든 pane 이다. floor 가 없으면(pre 훅이 안 돌았으면) 아무것도 안 한다.
- 시작 명령에 `resurrectRestoreMark` 가 있다 — 살아 있는 패널이나 그 뒤에 시작한 다른 것이 아니다.
- 창의 유일한 pane 이 아니다 — `kill-pane` 이 곧 `kill-session` 이다(panel.md).

`select-pane -t =세션:창.pane` 이 세션의 현재 창을 바꾸지 않는다는 것(복원된 «현재 창»이 그대로여야 한다)은
`scripts/tmux-assumptions.sh` 12절이 단언한다.

훅 값은 mux 의 절대 경로를 담으므로 `RepairOwnedConfig` 가 패널 블록처럼 고쳐 쓴다
(`repairedResurrectBlock`). 블록에서 플러그인 로드 여부와 플러그인 경로를 다시 읽어 그대로 두고, 훅이 없던
옛 블록은 여기서 훅을 얻는다. 설정의 다른 곳에서 사용자가 같은 훅을 쓰면 우리 것은 넣지 않는다 — 옵션 하나에
명령 하나이고, 그 자리는 그 사람 것이다. 훅 경로는 실패를 말하지 않는다(`runResurrectHook`) — resurrect 가
그 출력을 세션을 기다리는 사용자 앞에 띄운다.

시험할 때의 함정: `script -qfc "tmux attach" /dev/null </dev/null` 로 붙인 클라이언트는 활성 pane 에 EOF 를
보내 셸을 끝낸다. pane 하나짜리 창이 통째로 사라져 훅이 창을 지운 것처럼 보인다. `sleep 20 | script …` 처럼
입력을 열어 둔다.

## 영역의 모양

```
# mux resurrect {
set -g @resurrect-capture-pane-contents 'on'
set -g @resurrect-hook-pre-restore-all "'<mux>' resurrect-hook pre"
set -g @resurrect-hook-post-restore-all "'<mux>' resurrect-hook post"
set -g @continuum-restore 'on'
run-shell "'<plugins>/tmux-resurrect/resurrect.tmux'"
run-shell "'<plugins>/tmux-continuum/continuum.tmux'"
# mux resurrect }
```

- **패널 영역과 다른 펜스다.** 패널 블록은 `setup-panel` 과 복구 경로(`RepairOwnedConfig`)가 통째로
  다시 쓴다. 누군가의 세션이 저장되느냐가 그 재생성에 딸려 가면 안 된다. 이 영역도 훅에 mux 경로를 담으므로
  복구 대상이지만, 자기 펜스 안에서 따로 고쳐진다.
- **tpm 을 거치지 않고 `run-shell` 로 직접 부른다.** tpm 은 「마지막 줄이어야 한다」는 로더를 요구하고,
  사용자에게 tpm 이 없으면 tpm 까지 깔아야 한다. 플러그인 디렉터리는 tpm 의 기본값(`~/.tmux/plugins`,
  XDG 설정이면 `…/tmux/plugins`)을 쓰므로 나중에 tpm 으로 옮겨도 그 자리에 있다.
- **resurrect 가 continuum 보다 먼저다.** continuum 은 resurrect 가 로드될 때 옵션으로 공개하는
  저장·복원 스크립트 경로를 읽는다.
- **두 겹 인용** — tmux 큰따옴표 안에 `/bin/sh` 작은따옴표. 이유는 [tmux-config.md](tmux-config.md).

## mux 가 시작할 때 복원한다 (`RestoreAtStartup`)

continuum 은 tmux **서버가 시작될 때** 복원한다. 재부팅 뒤에는 누가 tmux 를 건드리기 전까지 서버가 없다.
2026-11-01 의 보고: 부팅 뒤 `mux` 를 열면 목록이 비어 있고, 거기서 `mux` 세션을 만들려 하자 «중복»이라며
옛 세션들이 나타났다. 순서는 이렇다 — 빈 목록에서 만든 `new-session` 이 서버를 띄우고, 서버가 설정을 읽어
continuum 이 복원하고, 복원이 방금 친 이름과 같은 세션을 되살린다.

그래서 `runRoot` 와 `mux new` 는 그리기 전에 `restoreSavedSessions` 를 부른다. 서버가 없고(`has-session`
실패), 설정에 mux 의 영역과 복원 훅이 있고, 저장 파일이 있을 때만 움직인다. 나머지 경우는 예전 그대로 연다.

1. 시드 세션 하나로 서버를 띄운다 — 설정이 읽히고 continuum 이 «방금 시작한 서버»를 본다.
2. post 훅이 남기는 `@mux_restored` 를 기다린다. 이것이 «누가 돌렸든 복원이 끝났다»는 유일한 신호다.
3. `restoreGrace`(5초)가 지나도 pre 훅의 floor 가 없으면 — 복원이 시작조차 안 했으면 — resurrect 의 복원
   스크립트(`@resurrect-restore-script-path`)를 `run-shell` 로 직접 돌린다. continuum 은 다른 tmux 서버가
   떠 있으면 물러서기 때문이다(`-L` 테스트 서버 하나로 충분하다). floor 가 있으면 continuum 이 이미 돌고
   있으므로 두 번 돌리지 않는다.
4. 시드를 닫는다. 복원된 것이 없으면 세션이 하나도 안 남아 서버도 함께 끝난다 — mux 가 찾은 그 상태다.

resurrect 에도 시드 처리가 있다(«처음부터 복원»이면 세션 `0` 을 닫는다). 하지만 서버의 pane 이 딱 하나일
때만 그렇게 판단하고, 패널 훅이 새 세션마다 pane 을 하나 더 넣으므로 여기서는 적용되지 않는다.

**시드 이름은 저장된 어떤 세션 이름으로도 시작하면 안 된다** (`seedSessionName`). resurrect 는
`has-session -t <이름>` 을 `=` 없이 묻고, tmux 는 정확한 이름이 없으면 접두사로 찾는다. 시드가
`mux-restoring` 이었을 때 저장된 `mux` 가 «있다»고 판정되어 그 창들이 시드 안으로 복원됐고, 시드를 닫자
함께 사라졌다 — 격리 서버에서 세 세션 중 둘만 돌아왔다. 첫 글자를 바꿔 가며 고른다. `_` 를 앞에 계속 붙이는
방식은 `_` 라는 세션 하나에 무한 루프가 된다(테스트가 실제로 멈췄다).

서버가 없으니 `@resurrect-dir` 은 읽을 수 없고 `resurrectDir` 은 resurrect 의 기본값으로 떨어진다. mux 의
영역은 그 옵션을 쓰지 않으므로 같은 곳이다.

시험할 때: `TMUX_TMPDIR` 로 기본 소켓을 옮기면 mux 의 `tmux` 호출이 전부 격리 서버로 간다. 다만 소켓 경로는
유닉스 소켓 길이 제한(약 108바이트)에 걸리므로 짧은 디렉터리를 쓴다 — 스크래치 경로는 «File name too long» 이었다.

## 플러그인

- **고정 리비전으로 clone 한다** (`resurrectPlugins`). 영역은 설정을 읽을 때마다 플러그인 코드를 실행하므로,
  「오늘 기본 브랜치가 가리키는 것」을 묻지도 않고 남의 머신에 받아 둘 수는 없다. 올리려면 리비전을
  바꾸고 격리 서버에서 저장→서버 종료→복원→창 선택을 다시 돌려 본다.
- **이미 있는 플러그인 디렉터리는 건드리지 않는다.** 어떻게 거기 왔든 사용자의 것이다. 디렉터리는 있는데
  진입 파일이 없으면 지우지 않고 보고한다.
- **플러그인이 디스크에 없으면 영역을 쓰지 않는다.** 없는 파일을 `run-shell` 하면 설정을 읽을 때마다 상태
  줄에 오류가 뜬다 — 영역이 없는 것보다 나쁘다.

## 이미 resurrect 를 쓰는 사용자

설정에 주석이 아닌 줄로 `tmux-resurrect` 가 이미 나오면(tpm `@plugin`, 손으로 쓴 `run-shell`) 영역은
pane 내용 저장 한 줄만 담는다. 두 번 로드하면 continuum 의 자동 저장이 겹치고, `@continuum-restore` 는
그 사용자가 고를 일이다. 주석 처리된 플러그인은 «껐다»는 뜻이지 «이미 있다»가 아니므로 세지 않는다.

## 첫 실행 제안과 실패

첫 실행 제안의 `y` 와 `mux setup` 은 이 영역을 마지막에 쓰고, 이것만 따로 실패할 수 있다
(`ErrResurrectUnavailable`). git 과 네트워크가 필요하고, 그 둘이 없다고 키 바인딩까지 빼앗을 이유는
없다. 그때 TUI 는 «설정 실패»가 아니라 «키는 됐고 저장·복원은 안 됐다»고 말한다 — 앞의 것은 이미 된
부분을 다시 하게 만든다.

테스트는 `withMock` 아래에서 `InstallIntegration` 을 부른다. 목 러너가 아니면 `git clone` 이 실제로
GitHub 에 나간다 — 이 기능을 넣을 때 기존 `TestInstallIntegrationWritesBothRegions` 가 그렇게 조용히
통과했다.

## continuum 이 스스로 물러서는 경우

- **다른 tmux 서버가 떠 있으면 자동 저장을 걸지 않는다**(`another_tmux_server_running`). 저장 파일끼리
  덮어쓰지 않으려는 것이다. `-L` 격리 서버(테스트, `scripts/tui-snapshot.sh`)가 떠 있는 동안 시작한
  서버가 여기에 해당한다.
- **자동 복원은 서버가 시작된 직후에만 한다**(`just_started_tmux_server`). 그래서 이미 돌고 있는 서버에
  영역을 적용해도 세션이 겹쳐 복원되지 않는다.
- **자동 저장은 `status-right` 에 끼워 넣은 보간으로 돈다.** 영역 뒤에서 `status-right` 를 통째로 다시
  설정하는 테마가 있으면 저장이 조용히 멈춘다.
