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

별도 훅 스크립트(복원 전 가장 큰 pane id 를 적어 두고, 복원 뒤 저장 파일에서 `mux panel` 이던 자리를
닫는 것)도 만들어 돌려 봤다. 동작은 했지만 기존 유령 판정과 같은 일을 두 번 하는 셈이라 버렸다.

## 영역의 모양

```
# mux resurrect {
set -g @resurrect-capture-pane-contents 'on'
set -g @continuum-restore 'on'
run-shell "'<plugins>/tmux-resurrect/resurrect.tmux'"
run-shell "'<plugins>/tmux-continuum/continuum.tmux'"
# mux resurrect }
```

- **패널 영역과 다른 펜스다.** 패널 블록은 `setup-panel` 과 복구 경로(`RepairOwnedConfig`)가 통째로
  다시 쓴다. 누군가의 세션이 저장되느냐가 그 재생성에 딸려 가면 안 된다. mux 경로가 들어 있지 않으므로
  복구 대상도 아니다.
- **tpm 을 거치지 않고 `run-shell` 로 직접 부른다.** tpm 은 「마지막 줄이어야 한다」는 로더를 요구하고,
  사용자에게 tpm 이 없으면 tpm 까지 깔아야 한다. 플러그인 디렉터리는 tpm 의 기본값(`~/.tmux/plugins`,
  XDG 설정이면 `…/tmux/plugins`)을 쓰므로 나중에 tpm 으로 옮겨도 그 자리에 있다.
- **resurrect 가 continuum 보다 먼저다.** continuum 은 resurrect 가 로드될 때 옵션으로 공개하는
  저장·복원 스크립트 경로를 읽는다.
- **두 겹 인용** — tmux 큰따옴표 안에 `/bin/sh` 작은따옴표. 이유는 [tmux-config.md](tmux-config.md).

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
