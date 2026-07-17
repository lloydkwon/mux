# mux

**AI CLI 세션 간 전환을 빠르고 직관적으로.**

tmux 세션을 터미널에서 빠르게 탐색하고 관리하는 TUI 도구입니다.

[English](README.md)

![Go](https://img.shields.io/badge/Go-1.21+-00ADD8?style=flat&logo=go)
![License](https://img.shields.io/badge/License-MIT-blue.svg)

![Demo](assets/demo.gif)

## 기능

- **실시간 프리뷰** — 선택한 세션의 터미널 출력을 우측 패널에 실시간으로 표시 (500ms 주기 갱신). `Tab`으로 세션을 펼쳐서 윈도우·페인 단위로 각각 프리뷰
- **AI CLI 감지** — `claude`, `codex`, `aider`, `gemini` 등의 AI CLI가 실행 중이면 배지로 표시
- **Git 브랜치 표시** — 각 세션의 현재 브랜치를 표시, worktree는 `⌥⌥`로 구분
- **비용/토큰 추적** — Claude Code 세션의 토큰 사용량과 예상 비용을 실시간 표시 (설정 불필요)
- **상태바 위젯** — `mux status`로 tmux 상태바에 AI 세션 아이콘 표시
- **팝업 오버레이** — AI CLI 실행 중에도 키 하나로 mux를 띄워 세션 전환
- **세션 관리** — TUI 내에서 생성/삭제/이름 변경
- **퀵 필터** — `/` 키로 세션 이름 또는 경로를 실시간 필터링

## 빠른 시작

```bash
# 인터랙티브 설치 (추천)
curl -sSL https://raw.githubusercontent.com/lunemis/mux/main/install.sh | bash

# 또는 직접 설치
brew install lunemis/tap/mux   # or: go install github.com/lunemis/mux/cmd/mux@latest
mux                             # 세션 매니저 실행
```

팝업 모드 설정 (tmux 위에 오버레이로 띄우기):

```bash
mux setup-keybind               # prefix + m 바인딩
tmux source-file ~/.tmux.conf   # 설정 리로드
```

이제 tmux에서 `Ctrl+b` → `m`으로 mux를 열 수 있습니다.

## 설치

### 인터랙티브 설치 (추천)

바이너리 설치와 키바인딩 설정을 안내합니다:

```bash
curl -sSL https://raw.githubusercontent.com/lunemis/mux/main/install.sh | bash
```

### Homebrew

```bash
brew install lunemis/tap/mux
```

### 소스에서 빌드

```bash
git clone https://github.com/lunemis/mux.git
cd mux
make install   # /usr/local/bin에 설치
```

### Go install

```bash
go install github.com/lunemis/mux/cmd/mux@latest
```

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
| `q` | 종료 |

## 요구사항

- tmux (팝업 모드는 3.2+)
- Linux 또는 macOS

## 기여

[CONTRIBUTING.md](CONTRIBUTING.md)를 참고하세요.

## 라이선스

[MIT](LICENSE)
