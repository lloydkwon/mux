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
- **터미널 색을 따름** — mux는 hex를 박지 않고 ANSI 팔레트 색으로 그립니다. 밝은 스킴이든 어두운 스킴이든 그 스킴이 고른 값이 쓰이므로 설정할 것이 없습니다. 선택된 행은 반전(reverse)이라 어떤 스킴에서도 읽힙니다
- **Git 브랜치 표시** — 각 세션의 현재 브랜치를 표시, worktree는 `⌥⌥`로 구분
- **비용/토큰 추적** — Claude Code 세션의 토큰 사용량과 예상 비용을 실시간 표시 (설정 불필요)
- **AI 진행 상태** — 작업 중(`⏳`) / 승인 대기(`❗`) / 입력 대기(`✅`)를 경과 시간과 함께 AI 배지 자리에 표시. 화면 출력을 추측하지 않고 도구가 쓰는 상태 파일을 직접 읽음 (현재 Claude Code)
- **상시 패널** — `mux watch`가 창 왼쪽 좁은 pane에 세션 목록과 상태 변화 로그를 상시 표시하고, 나머지는 그대로 내 터미널. 클릭하면 그 세션으로 이동, `prefix + Tab`으로 패널을 오가며 키보드로 고르기, `mux nav`로는 포커스를 안 뺏고 커서만 이동
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

목록은 각 세션이 *지금* 무엇을 하는지는 보여주지만 방금 무엇을 끝냈는지는 남기지 않습니다. `mux watch`는 창 **왼쪽**의 좁은 pane을 세션 목록과 최근 상태 변화 로그로 채워, TUI를 열었을 때만이 아니라 작업하는 내내 보이게 합니다. 나머지는 그대로 내 터미널입니다:

```
┌ mux watch 48 ──┬ 내 셸 ──────────────────────┐
│ 🔔 AI 세션     │ ❯ npm test                  │
│                │ ✓ 42 passed                 │
│ ❗ api 12s     │                             │
│    permission  │ ❯ _                         │
│                │                             │
│ ⏳ mux 3m ◀    │                             │
│    13:02 ⏳    │                             │
│    12:58 ✅    │                             │
│    12:41 ⏳    │                             │
│                │                             │
│ ✅ web 2h      │                             │
│    11:20 ✅    │                             │
│                │                             │
│ ── 세션        │                             │
│                │                             │
│    notes 3h    │                             │
└────────────────┴─────────────────────────────┘
```

AI CLI가 도는 세션이 먼저 오고, 나머지는 `── 세션` 아래에 붙습니다 — TUI를 열지 않고도 모든 세션에 갈 수 있습니다.

**모든 세션이 마지막에 무슨 일이 있었는지 한 줄로 말하고, 커서가 놓인 세션만 이력을 펼칩니다.** 예전에는 목록 아래에 시간순 로그 하나가 깔렸는데, 실제 서버에서는 그게 제일 바쁜 세션 하나가 같은 말을 반복하는 마흔 줄이었습니다 — *이* 세션에 무슨 일이 있었는지 보려면 그걸 다 지나쳐야 했습니다. 이제 각 세션의 전환은 자기 행 아래에 붙습니다 — 모두에게 한 줄씩, 커서가 놓인 곳에는 이력 전체. 공유 로그는 50건 상한에 밀려 사라질 세션의 최신 한 건을 남기므로, 바쁜 세션이 조용한 세션을 목록에서 통째로 지워버리지 않습니다. 새로 누를 것은 없습니다: `M-Up`/`M-Down`이 이미 커서를 옮기고, 클릭과 `enter`는 여전히 "이 세션으로 이동"입니다. 이력은 세션 목록이 쓰고 남은 줄만, 그것도 상한을 두고 씁니다 — pane은 아래에서 말없이 잘리고, 남아야 할 것은 세션 쪽이기 때문입니다.

"작업 중"으로 들어가는 전환은 일부러 기록하지 않습니다 — 배지가 이미 말하고 있고, 턴마다 한 줄씩 쌓이면 정작 중요한 두 개가 묻힙니다.

**클릭하면 그 세션으로 이동합니다.** 먼저 읽을 것이 없습니다 — 패널은 일부러 어떤 세션의 화면도 복사해 그리지 않습니다. 정작 보고 싶은 그 화면은 대개 바로 옆 pane에 이미 떠 있으니까요.

패널이 들어 있는 그 세션은 `◀`로 표시되고, **커서도 거기서 시작합니다** — 하이라이트와 표시가 같은 행에 옵니다. 패널은 창마다 하나씩이라 세션을 전환하면 아무것도 들은 적 없는 새 패널 앞에 서게 되는데, 그 패널이 무엇을 고른 채 열리느냐가 "지금 어디인지"를 말해 주는 유일한 신호입니다. 그 행에서 Enter를 눌러도 헛되지 않습니다 — 작업하던 pane으로 포커스를 돌려줍니다.

**지금 있는 세션의 요약은 작업 pane 위 테두리에 붙습니다.** 이름·경로·AI 도구와 그 상태·git 브랜치 — `prefix + m`을 눌러 화면을 덮지 않고도 보이게 하려는 그 한 줄입니다. `mux setup-panel`이 tmux의 `pane-border-status`를 켜고 `pane-border-format`이 새 `mux border`를 부르게 합니다:

```
┌ mux watch ─────┬ [ mux ] ~/Projects/temp/mux  ✅ claude  ⌥ main ─┐
│ 🔔 AI 세션     │ ❯ npm test                                      │
│                │ ✓ 42 passed                                     │
│ ⏳ mux 3m ◀    │ ❯ _                                             │
└────────────────┴─────────────────────────────────────────────────┘
```

테두리인 이유는 그 아래가 내 셸이기 때문입니다 — mux가 글자를 쓸 수 없고, pane 제목은 거기서 도는 프로그램(예: Claude Code가 붙이는 작업 이름)의 것입니다. 패널 pane 위에는 뜨지 않습니다: 목록 바로 위에 같은 말을 두 번 할 이유가 없으니까요. pane이 좁아지면 브랜치 → 도구 이름 → 경로 순으로 줄어들고 세션 이름은 끝까지 남습니다. **pane마다 세로 한 줄**을 씁니다.

**키보드로 쓰는 길은 두 가지이고, `mux setup-panel`이 둘 다 깔아줍니다.**

`prefix + Tab`은 패널 안으로 들어갔다 나옵니다. 들어가 있는 동안에는 패널 자체 키가 그대로 먹습니다 — `j`/`k`로 이동, `enter`로 그 세션으로, `esc`로 아무것도 안 고르고 나오기. `enter`를 누르면 새 세션으로 가는 길에 포커스를 원래 pane으로 돌려주므로 왕복을 신경 쓸 필요가 없습니다.

`M-Up` / `M-Down` 과 `prefix + Enter` 는 같은 조작을 **포커스를 전혀 옮기지 않고** 합니다. `send-keys`로 패널에 닿기 때문에 내 pane에서 계속 타이핑하면서 커서만 움직입니다. 이쪽이 여전히 패널의 기본 사용법이고, 포커스 키는 목록을 제대로 훑어보고 싶을 때를 위한 것입니다.

```tmux
bind Tab        run-shell "/mux/절대경로 panel --focus -t #{pane_id}"
bind -n M-Up    run-shell "/mux/절대경로 nav -t #{pane_id} up"
bind -n M-Down  run-shell "/mux/절대경로 nav -t #{pane_id} down"
bind Enter      run-shell "/mux/절대경로 nav -t #{pane_id} enter"
```

`nav`의 방향은 `up`, `down`, `top`, `bottom`, `enter` 다섯 가지입니다. 패널이 없는 창에서는 둘 다 아무 일도 하지 않고 정상 종료하므로 전역 바인딩으로 둬도 안전합니다.

패널 안에서 `q`는 나가는 게 아니라 패널을 **닫습니다** — 나가는 키는 `esc`입니다. 실수로 닫혔더라도 다음 창·세션 전환 때 훅이 다시 열고, `prefix + a`로도 바로 열립니다.

패널은 pane이고 pane은 창 하나에 속합니다 — 그래서 **패널을 가져 본 적 없는 창에는 안 보입니다.** 패널에서 세션을 클릭하면 바로 그런 창으로 가게 되고, 패널이 사라진 것처럼 보입니다. `mux setup-panel`이 바인딩과 훅을 한 번에 깔아 줍니다:

```bash
mux setup-panel                 # prefix + a 로 토글, prefix + Tab 으로 패널에 들어갔다 나오기
                                # 둘 다 바꾸려면: mux setup-panel a Tab
tmux source-file ~/.tmux.conf
```

절대경로를 채운 `# mux panel { … }` 블록을 씁니다. 다시 실행하면 그 블록을 **교체**하지 쌓지 않고, [oh-my-tmux](https://github.com/gpakosz/.tmux) 사용자는 `.tmux.conf.local`의 센티널 앞으로 보냅니다. 설정 끝이 tpm 로더로 끝나면(tpm이 마지막 줄을 요구합니다) 그 위에 넣습니다.

설치되는 훅은 `after-new-window`, `after-new-session`, `after-select-window`, `client-attached`, `client-session-changed`, `client-resized`, `after-resize-window` 일곱 개이고 모두 같은 `mux panel --auto`를 부릅니다. **세션을 옮겨도 패널이 따라오게 하는 건 `client-session-changed`** 이고, 리사이즈 두 개는 좁아서 물러났던 창이 넓어졌을 때 되살리는 몫입니다.

직접 쓰고 싶다면:

```tmux
bind a run-shell "/mux/절대경로 panel -t #{pane_id}"
set-hook -g client-session-changed 'run-shell "/mux/절대경로 panel --auto -t #{pane_id}"'
# … 나머지 훅도 같은 줄을 하나씩
```

`--auto`는 훅 전용 표시입니다. 토글이 아니라 **ensure**로 — 없으면 만들고 있으면 닫지 않습니다 — 그래서 리사이즈 훅이 같은 커맨드를 불러도 패널이 껐다 켜졌다 하지 않습니다. 세 경우에 물러납니다: **폭 140칸 미만인 창**, **VS Code 통합 터미널로만 보고 있는 세션**, 그리고 **직접 닫아둔 창**. 키를 직접 누르면 셋 다 무시하고 열리며(기본값이 아니라 결정이므로), 수동 끄기 표시도 지워져 훅이 다시 동작합니다.

**[tmux-resurrect](https://github.com/tmux-plugins/tmux-resurrect)로 복원하면 패널은 빈 셸로 돌아옵니다.** 이 플러그인은 pane의 **자식** 프로세스를 저장하는데 패널은 자식이 없습니다 — 패널 자체가 pane의 프로세스입니다. 그래서 되살릴 것이 기록되지 않고 `@resurrect-processes`로도 해결되지 않습니다. mux는 자기 pane에 tmux pane title을 달아 두고(복원이 되돌려주는 유일한 것입니다) 그 죽은 pane을 닫은 뒤 그 자리에 살아 있는 패널을 엽니다. 설정할 것은 없습니다. 이 기능 이전에 만들어진 패널만 처음 한 번 인식되지 않습니다.

**패널이 안 보이면 창 너비부터 의심하세요.** 물러날 때 아무 말도 하지 않는 것은 의도된 설계입니다 — 훅은 쉴 새 없이 발화하므로 창마다 에러를 뱉는 편이 더 나쁩니다. 140은 패널의 48칸 + 작업 pane에 남길 만큼이고, 열려 있는 패널이 스스로 나가는 기준도 같은 값이라 열지 않았을 창에서 사용자를 밀어내는 일이 없습니다. 화면에 맞춰 조절하려면:

```bash
tmux set -g @mux_panel_min_width 120
```

폭 기준이 곧 "작은 화면이 아님"의 판정입니다. 모바일 SSH 클라이언트는 VS Code처럼 환경변수로 식별할 수 없고, 애초에 문제는 기기가 아니라 폭입니다 — `aggressive-resize` 때문에 휴대폰으로 붙으면 창이 54칸쯤으로 줄어드는데 패널은 48을 쥐고 있어 작업 pane에 5칸만 남습니다. 이 기준은 실제로 쓰는 터미널도 갈라줍니다(VS Code 통합 터미널 약 150칸, 전체 터미널 269칸). 또한 환경변수 검사가 닿지 못하는 곳까지 미칩니다 — 클라이언트가 붙어 있지 않은 창은 들여다볼 대상이 없지만 크기는 있습니다.

창이 기준 아래로 좁아지면 열려 있던 패널은 **스스로 닫히고**, 다시 넓어지면 리사이즈 훅이 **되살립니다**.

클라이언트별로 숨기는 것은 불가능합니다: pane은 클라이언트가 아니라 윈도우에 속하므로 두 터미널에서 같이 보는 창은 양쪽 모두에 보이고, 조절할 수 있는 건 **존재 여부**뿐입니다.

`mux panel`은 그 윈도우에 패널이 있으면 닫고 없으면 엽니다. 그래서 같은 키로 숨길 수 있고, 두 번 눌러도 두 개가 생기지 않습니다. 갓 만들어진 윈도우에는 패널이 있을 수 없으므로 훅도 같은 커맨드를 씁니다 — "열기 전용" 변형이 따로 필요 없습니다.

포커스는 작업하던 pane에 남습니다 — 패널이 열릴 때도, 패널을 클릭한 뒤에도.

패널 폭은 pane 테두리를 드래그해 조절합니다 — 테두리 드래그는 tmux가 직접 처리하고 pane으로 넘기지 않으므로 클릭 기능과 충돌하지 않습니다. 조절한 폭은 **세션별로 기억**되어(tmux user option에 저장, 세션과 수명이 같음) 다시 열 때 그 폭으로 나옵니다. 처음 여는 패널은 48칸입니다.

행은 **화면에 찍히는 값 순서**, 곧 그 상태를 얼마나 오래 쥐고 있었는지 순으로 최근이 위에 옵니다. 다른 기준으로 정렬하면 그 칸이 단조롭지 않게 되는데, 41m짜리가 3h짜리 둘 밑에 있으면 고장으로 읽힙니다. 상태가 바뀌면 행은 실제로 움직이고, 그래서 세션 블록에는 아래 빈 줄이 포함됩니다 — 클릭 표적이 한 줄이 아니라 두 줄입니다.

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
| 클릭 | 그 행으로 커서 이동 + 오른쪽에 프리뷰 |
| 더블클릭 | attach — 그 행에서 `Enter`와 동일 |
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

`?`를 누르면 목록 행에 붙는 모든 표기(`▶`/`▼`, attach 중인 세션의 밝은 이름, `#N`, 경과 시간 칸, `⌥`, AI 배지)를 위 단축키와 함께 설명하는 전체 화면이 열린다. 하단 footer는 단축키를 나열할 뿐 표기의 뜻까지는 알려주지 못한다.

## 요구사항

- tmux (팝업 모드는 3.2+)
- Linux 또는 macOS

## 기여

[CONTRIBUTING.md](CONTRIBUTING.md)를 참고하세요.

## 라이선스

[MIT](LICENSE)
