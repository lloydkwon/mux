# mux

**AI CLI 세션 간 전환을 빠르고 직관적으로.**

> [!IMPORTANT]
> 이 저장소는 포크 체인의 세 번째입니다:
> [lunemis/mux](https://github.com/lunemis/mux) →
> [xguru/mux](https://github.com/xguru/mux) → 이 저장소.
>
> 라이브 프리뷰, AI CLI 감지, Git/worktree 표시, popup 모드와 핵심 세션 관리
> 기능은 lunemis/mux와 기여자들이 만들었습니다. xguru/mux가 SSH 우선 시작
> 메뉴와 세션 순서 지정을 더했고, 이 포크는 여기에 Claude Code 진행 상태
> 표시를 추가했습니다.

tmux 세션을 터미널에서 빠르게 탐색하고 관리하는 TUI 도구입니다.

[English](README.md)

![Go](https://img.shields.io/badge/Go-1.21+-00ADD8?style=flat&logo=go)
![License](https://img.shields.io/badge/License-MIT-blue.svg)

![Demo](assets/demo.gif)

## 개인 포크에서 추가한 기능

### xguru/mux에서 이어받은 것

- **New shell** — SSH 로그인 직후 mux가 열렸을 때 일반 로그인 셸로
  들어갑니다. tmux 안이나 mux popup에서 선택하면 세션은 유지한 채 현재
  client를 detach하고 바깥 셸로 돌아갑니다.
- **New tmux session** — 이름과 시작 디렉터리를 지정해 새 세션을 만들고
  바로 attach합니다.
- **영속적인 Order** — 세션을 선택하고 숫자를 입력한 뒤 `Enter`를 누르면
  원하는 순서를 지정합니다. `0`은 Order를 해제합니다.
- **정렬 전환** — `o`를 누를 때마다 최근 활동 순, 알파벳 순, Order 순으로
  정렬 방식이 바뀝니다.

### 이 포크에서 추가한 것

- **AI 배지가 라이브 상태까지 표현** — 어떤 AI CLI가 도는지 알려주던 기존
  배지가, 이제 그 도구가 무엇을 하는 중인지도 함께 알려줍니다. 작업
  중(`⏳`), 사용자 입력을 기다리며 막힘(`❗`), 턴이 끝나 입력 대기(`✅`)를
  경과 시간과 함께 표시하고, 상태를 발행하지 않는 도구는 원래 아이콘
  (`✦ ◈ ⬡ ✧`)을 유지합니다. 프리뷰에는 무엇 때문에 막혔는지(권한 승인
  프롬프트, 입력 필요 등)를 덧붙이고, `mux status`도 tmux 상태바에 같은
  배지를 찍습니다.

개인 포크 기능이 필요하지 않고 원작과 Homebrew 패키지를 사용하려면
[lunemis/mux](https://github.com/lunemis/mux)를 이용해 주세요.

## 기능

- **실시간 프리뷰** — 선택한 세션의 터미널 출력을 우측 패널에 실시간으로 표시 (500ms 주기 갱신). `Tab`으로 세션을 펼쳐서 윈도우·페인 단위로 각각 프리뷰
- **AI CLI 감지** — `claude`, `codex`, `aider`, `gemini` 등의 AI CLI가 실행 중이면 배지로 표시. 도구가 상태를 발행하면(현재 Claude Code) 같은 배지가 진행 상태(`⏳`/`❗`/`✅`)로 바뀜
- **Git 브랜치 표시** — 각 세션의 현재 브랜치를 표시, worktree는 `⌥⌥`로 구분
- **비용/토큰 추적** — Claude Code 세션의 토큰 사용량과 예상 비용을 실시간 표시 (설정 불필요)
- **AI 진행 상태** — 작업 중(`⏳`) / 승인 대기(`❗`) / 입력 대기(`✅`)를 경과 시간과 함께 AI 배지 자리에 표시. 화면 출력을 추측하지 않고 도구가 쓰는 상태 파일을 직접 읽음 (현재 Claude Code)
- **알림 패널** — `mux watch`가 전용 pane에 AI 세션 요약과 최근 상태 변화 로그를 상시 표시. 세션 행을 클릭하면 그 세션으로 전환
- **상태바 위젯** — `mux status`로 tmux 상태바에 AI 세션 배지 표시 (상태가 있으면 상태 글리프로)
- **팝업 오버레이** — AI CLI 실행 중에도 키 하나로 mux를 띄워 세션 전환
- **세션 관리** — TUI 내에서 생성/삭제/이름 변경
- **퀵 필터** — `/` 키로 세션 이름 또는 경로를 실시간 필터링

## 빠른 시작

```bash
go install github.com/lloydkwon/mux/cmd/mux@latest
mux
```

팝업 모드 설정 (tmux 위에 오버레이로 띄우기):

```bash
mux setup-keybind               # prefix + m 바인딩
tmux source-file ~/.tmux.conf   # 설정 리로드
```

이제 tmux에서 `Ctrl+b` → `m`으로 mux를 열 수 있습니다.

## 설치

### 인터랙티브 설치

Go가 있으면 Go로 빌드하고, 없으면 릴리스 바이너리를 내려받습니다. 팝업
키바인딩 설정도 함께 물어봅니다.

```bash
curl -sSL https://raw.githubusercontent.com/lloydkwon/mux/main/install.sh | bash
```

### Go install

```bash
go install github.com/lloydkwon/mux/cmd/mux@latest
```

체크아웃한 작업 트리를 그대로 빌드하려면 저장소 루트에서
`go install ./cmd/mux`를 실행하세요.

### 소스에서 빌드

```bash
git clone https://github.com/lloydkwon/mux.git
cd mux
make PREFIX="$HOME/.local" install
```

`$HOME/.local/bin/mux`에 설치됩니다. 기본 prefix인 `/usr/local`을 쓰려면
`sudo make install`을 실행하세요.

### 원작 Homebrew 패키지

```bash
brew install lunemis/tap/mux
```

> 이 명령은 포크 기능이 없는 원작 버전을 설치합니다.

## 사용법

### 기본

`mux`를 실행하면 세션 매니저가 열립니다. `j`/`k`로 탐색, `Enter`로 attach, `q`로 종료.

![Screenshot](assets/screenshot.png)

왼쪽 패널에 세션 목록(AI 배지 + git 브랜치), 오른쪽 패널에 선택한 세션의 **실시간 프리뷰**가 500ms마다 갱신됩니다.

목록 상단의 **New shell**은 mux를 닫고 현재 로그인 셸을 계속 사용합니다. tmux 안이나 mux popup에서 선택하면 세션을 종료하지 않고 현재 tmux client를 detach해 바깥 로그인 셸로 돌아갑니다. **New tmux session**은 이름과 시작 디렉터리를 입력받아 새 tmux 세션을 만들고 바로 attach합니다.

세션 행을 선택한 상태에서 숫자를 누르면 영속적인 Order 입력 모드가 열립니다. 여러 자리 숫자를 이어서 입력하고 `Enter`로 저장하며, `0`은 Order를 해제합니다. `o`를 누르면 최근 활동 순 → 세션명 순 → Order 순으로 정렬 방식이 순환합니다. 설정은 `~/.config/mux/preferences.json`(또는 플랫폼의 사용자 설정 디렉터리)에 저장됩니다.

### 팝업 모드 (추천)

tmux 안에서 작업 중일 때, 어떤 프로그램이 실행 중이어도 키 하나로 mux를 오버레이로 띄울 수 있습니다.

```bash
# 키바인딩 설정 (최초 1회)
mux setup-keybind          # prefix + m (기본값)
mux setup-keybind Space    # 다른 키로 변경 가능

# tmux 설정 리로드
tmux source-file ~/.tmux.conf
```

`mux popup`으로 수동 실행도 가능합니다.

> **참고:** tmux 3.2 이상 필요

![Popup mode](assets/popup.gif)

### 상태바 위젯

TUI를 열지 않고 tmux 상태바에서 AI 세션 아이콘을 표시:

```bash
# ~/.tmux.conf에 추가
set -g status-right '#(mux status)'
```

AI 세션이 활성화되면 `✦ ◈` 같은 아이콘이 상태바에 표시됩니다.

### 상시 패널 — `mux watch`

목록은 각 세션이 *지금* 무엇을 하는지는 보여주지만 방금 무엇을 끝냈는지는 남기지 않습니다. `mux watch`는 전용 pane을 AI 세션 요약과 최근 상태 변화 로그로 채워, TUI를 열었을 때만이 아니라 작업하는 내내 보이게 합니다:

```
 🔔 AI 세션

 ⏳ mux 2m                       ⌥ main

 ❗ api 30s                 ⌥ feature/auth
    Bash: git push --force

 ── 최근 이벤트
 13:42:06 api ❗ 승인 대기 · Bash: git...
 13:40:46 mux ✅ 작업 완료
```

"작업 중"으로 들어가는 전환은 일부러 기록하지 않습니다 — 배지가 이미 말하고 있고, 턴마다 한 줄씩 쌓이면 정작 중요한 두 개가 묻힙니다.

```tmux
# ~/.tmux.conf에 추가 — prefix+a로 현재 윈도우의 패널을 켜고 끈다
bind a run-shell "/mux/절대경로 panel -t #{pane_id}"

# 새 윈도우·새 세션에는 자동으로 붙인다
set-hook -g after-new-window  'run-shell "/mux/절대경로 panel --auto -t #{pane_id}"'
set-hook -g after-new-session 'run-shell "/mux/절대경로 panel --auto -t #{pane_id}"'
```

`--auto`는 훅 전용 표시로, 두 경우에 물러납니다 — **VS Code 통합 터미널로만 보고 있는 세션**, 그리고 **폭이 96칸 미만인 창**. 어느 쪽이든 키를 직접 누르면 열립니다(기본값이 아니라 결정이므로).

폭 기준이 곧 "모바일이 아님"의 판정입니다. 모바일 SSH 클라이언트는 VS Code처럼 환경변수로 식별할 수 없고, 애초에 문제는 기기가 아니라 폭입니다 — `aggressive-resize` 때문에 휴대폰으로 붙으면 창이 54칸쯤으로 줄어드는데 패널은 48을 쥐고 있어 작업 pane에 5칸만 남습니다. 그 상황이 되면 이미 열려 있던 패널은 **스스로 닫히고**, 넓은 화면에서 `prefix+a`로 다시 엽니다.

클라이언트별로 숨기는 것은 불가능합니다: pane은 클라이언트가 아니라 윈도우에 속하므로 두 터미널에서 같이 보는 창은 양쪽 모두에 보이고, 조절할 수 있는 건 **존재 여부**뿐입니다.

`mux panel`은 그 윈도우에 패널이 있으면 닫고 없으면 엽니다. 그래서 같은 키로 숨길 수 있고, 두 번 눌러도 두 개가 생기지 않습니다. 갓 만들어진 윈도우에는 패널이 있을 수 없으므로 훅도 같은 커맨드를 씁니다 — "열기 전용" 변형이 따로 필요 없습니다.

포커스는 작업하던 pane에 남습니다. **세션 행을 클릭하면 그 세션으로 전환됩니다.** 맨 아래 줄에는 갱신 주기와 마지막 확인 시각이 표시됩니다.

패널 폭은 pane 테두리를 드래그해 조절합니다 — 테두리 드래그는 tmux가 직접 처리하고 pane으로 넘기지 않으므로 클릭 기능과 충돌하지 않습니다. 조절한 폭은 **세션별로 기억**되어(tmux user option에 저장, 세션과 수명이 같음) 다시 열 때 그 폭으로 나옵니다.

세션은 **생성이 최근인 순**으로 나열되고, 보는 동안 순서가 바뀌지 않습니다. AI 상태 나이처럼 계속 변하는 값으로 정렬하면 2초마다 행이 재배치되는데, 클릭 이동이 있는 상태에서 행이 움직이면 엉뚱한 세션으로 전환됩니다.

클릭을 받으려면 마우스 리포팅을 켜야 하는데, 그러면 tmux가 그 pane의 마우스 이벤트를 전부 넘겨주므로 **그 pane에서만** tmux의 휠 스크롤(copy-mode)과 드래그 선택이 동작하지 않습니다. 텍스트 선택은 Shift를 누른 채 하면 됩니다.

**팝업이 아니라 pane이어야 합니다.** tmux에는 키보드를 안 뺏는 떠 있는 창이 없습니다. `display-popup`은 man 페이지에 *"Panes are not updated while a popup is present"*라고 명시돼 있습니다 — 뒤 pane을 얼리고 입력을 가져가므로 곁눈질용 오버레이로는 쓸 수 없습니다. 입력을 안 뺏으면서 항상 보이는 tmux 표면은 pane뿐입니다.

tmux가 보는 PATH에 `mux`가 없다면 절대경로를 쓰세요 — 버전 매니저 셰임은 보통 안 잡힙니다.

### skimd 연동

마크다운 뷰어 [skimd](https://github.com/lunemis/skimd)와 함께 쓰면 AI가 생성한 문서를 tmux 안에서 바로 검토할 수 있습니다.

- `prefix+m` → **mux** — 세션 전환
- `prefix+v` → **skimd** — 문서 훑기

![mux + skimd workflow](assets/workflow.gif)

### 키바인딩

| 키 | 동작 |
|---|---|
| `j` / `k` | 위로 / 아래로 이동 |
| `g` / `G` | 처음 / 마지막으로 이동 |
| `Tab` / `→` / `l` | 세션 → 윈도우 → 페인 펼치기 |
| `Shift+Tab` / `←` / `h` | 한 단계 접기 |
| `Enter` | attach (선택한 윈도우·페인까지 포커스) |
| `n` | 새 세션 생성 |
| `r` | 세션 이름 변경 |
| `x` | 세션 삭제 (확인 후) |
| `0`–`9` | 선택한 세션의 Order 설정 (`0`은 해제) |
| `o` | 정렬 순환: 최근 활동 → 세션명 → Order |
| `/` | 세션 필터링 |
| `Esc` | 필터 초기화 / 모드 취소 |
| `?` | 도움말 — 표기 범례와 전체 단축키 |
| `q` | 종료 |

`?`를 누르면 목록 행에 붙는 모든 표기(`▶`/`▼`, `*`/`○`, `#N`, 경과 시간 칸, `⌥`, AI 배지)를 위 단축키와 함께 설명하는 전체 화면이 열린다. 하단 footer는 단축키를 나열할 뿐 표기의 뜻까지는 알려주지 못한다.

## 요구사항

- tmux (팝업 모드는 3.2+)
- Linux 또는 macOS

## 기여

[CONTRIBUTING.md](CONTRIBUTING.md)를 참고하세요.

## 라이선스

[MIT](LICENSE)
