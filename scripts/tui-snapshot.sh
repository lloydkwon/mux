#!/usr/bin/env bash
# 작업 트리의 mux 를 격리된 tmux 서버에서 띄우고, 키를 보내고, 화면을 글자로 찍는다.
#
# 단위 테스트(골든 포함)는 러너를 목킹한 Model 을 그린다. 이 스크립트는 그 반대편이다 — 진짜
# tmux 위에서 진짜 바이너리가 그리는 화면을 본다. 사람이 화면을 보고서야 알던 것을 에이전트가
# 직접 확인하라고 있는 것이고, 그래서 출력은 사람이 아니라 모델이 읽기 좋은 평문이다.
#
# 사용:
#   scripts/tui-snapshot.sh                                  기본 픽스처, 120x30, 키 없음
#   scripts/tui-snapshot.sh --keys 'j j Tab' --size 100x24   키는 tmux send-keys 이름, 공백으로 나눈다
#   scripts/tui-snapshot.sh --cmd 'watch'                    mux 대신 `mux watch`(패널)를 띄운다
#   scripts/tui-snapshot.sh --ansi                           색까지 (capture-pane -e)
#   scripts/tui-snapshot.sh --keep                           끝나도 서버를 남긴다 (소켓 이름을 알려 준다)
#
# 격리는 세 겹이고 셋 다 필요하다 — 하나라도 빠지면 개발자의 환경을 고친다:
#   1. tmux 서버: -L 전용 소켓 + -f /dev/null. 사용자의 세션·훅·설정을 건드리지 않는다
#      (scripts/test-fixture.sh 는 «실제» 서버에 만든다 — 이건 그러지 않는다).
#   2. HOME: mux 는 시작할 때 ~/.tmux.conf 의 mux 영역이 다른 바이너리를 가리키면 «고친다»
#      (tmux/repair.go). 작업 트리 빌드를 그대로 띄우면 사용자의 tmux.conf 가 이 빌드를 가리키게 된다.
#   3. XDG_CONFIG_HOME: ~/.config/mux/server.json(되살리기 대상 서버 기록)·preferences.json·panel.json
#      을 쓴다. 격리 서버가 server.json 에 적히면 진짜 서버가 소켓을 잃었을 때 엉뚱한 pid 를 본다.
set -euo pipefail

SIZE=120x30
KEYS=""
CMD=""
ANSI=""
KEEP=""
WAIT=1.5
while [ $# -gt 0 ]; do
  case "$1" in
    --size) SIZE="$2"; shift 2 ;;
    --keys) KEYS="$2"; shift 2 ;;
    --cmd)  CMD="$2"; shift 2 ;;
    --ansi) ANSI="-e"; shift ;;
    --keep) KEEP=1; shift ;;
    --wait) WAIT="$2"; shift 2 ;;
    -h|--help) sed -n 2,21p "$0"; exit 0 ;;
    *) echo "알 수 없는 인자: $1" >&2; exit 2 ;;
  esac
done
W="${SIZE%x*}"; H="${SIZE#*x}"
command -v tmux >/dev/null || { echo "tmux 가 없다" >&2; exit 1; }

ROOT="$(cd "$(dirname "$0")/.." && pwd)"
TMP="$(mktemp -d "${TMPDIR:-/tmp}/mux-snap.XXXXXX")"
SOCK="mux-snap-$$"
BIN="$TMP/bin/mux"

# 환경은 허용 목록으로만 넘긴다(env -i). 서버는 띄운 클라이언트의 환경을 통째로 물려받으므로, 그대로 두면
# HERDR_SOCKET_PATH·ENV(dash 가 source 한다) 같은 것이 격리 서버의 모든 pane 에 따라 들어간다.
#   SHELL       서버의 default-shell 이 된다. 비어 있는 HOME 에서 zsh 는 첫 실행 마법사를 띄워 픽스처를 가린다
#   TMUX_TMPDIR 소켓도 임시 디렉터리 안에 — 사용자의 실제 소켓 디렉터리에 아무것도 만들지 않는다
T() {
  env -i PATH="$PATH" TERM="${TERM:-xterm-256color}" LANG="${LANG:-C.UTF-8}" \
    HOME="$TMP" XDG_CONFIG_HOME="$TMP/.config" SHELL=/bin/sh TMUX_TMPDIR="$TMP" \
    tmux -L "$SOCK" -f /dev/null "$@"
}
cleanup() {
  if [ -n "$KEEP" ]; then
    echo "남겨 둠: TMUX_TMPDIR=$TMP tmux -L $SOCK attach   (지우기: 같은 앞머리로 kill-server; rm -rf $TMP)" >&2
  else
    T kill-server 2>/dev/null || true
    rm -rf "$TMP"
  fi
}
# 빌드보다 먼저 건다 — 빌드가 실패해도 임시 디렉터리를 치운다
trap cleanup EXIT

# HOME 을 바꾸기 «전에» 빌드한다 — Go 빌드 캐시가 HOME 아래에 있다(T 밖에서 돌므로 원래 환경 그대로다)
(cd "$ROOT" && go build -o "$BIN" ./cmd/mux)

mkdir -p "$TMP/.config/mux" "$TMP/work"
# 첫 실행 제안(설정 y/n 페이지)을 건너뛴다 — 목록을 보려는 것이다
printf '{"sort":"name","setup_offered":true}\n' > "$TMP/.config/mux/preferences.json"

# 픽스처: 창 여럿, pane 여럿, 이름 긴 세션 — test-fixture.sh 와 같은 모양을 격리 서버에
T new-session -d -s alpha -n editor -x "$W" -y "$H" -c "$TMP/work"
T split-window -t alpha:editor -h -c "$TMP/work"
T new-window -t alpha -n logs -c "$TMP/work"
T send-keys -t alpha:logs "echo 'alpha logs: ready'" C-m
T new-session -d -s beta -x "$W" -y "$H" -c "$TMP/work"
T send-keys -t beta "printf 'beta line %s\n' 1 2 3" C-m
T new-session -d -s a-session-with-a-rather-long-name -x "$W" -y "$H" -c "$TMP/work"

# mux 는 자기 세션의 pane 안에서 돈다 — $TMUX 가 이 격리 서버를 가리키므로 mux 의 tmux 호출도 여기로 간다.
# 이름이 zz- 인 것은 이름순 맨 뒤로 보내기 위해서다: 커서가 그 행에 오면 프리뷰가 mux 자신의 화면을
# 거울처럼 다시 그려 픽스처를 보려던 화면을 가린다.
MUXCMD="MUX_NO_BOOTSTRAP=1 exec '$BIN' $CMD"
T new-session -d -s zz-snapshot -x "$W" -y "$H" -c "$TMP/work" "$MUXCMD"
sleep "$WAIT"

ended() { echo "mux 가 끝났다(세션 zz-snapshot 없음) — 키가 종료를 눌렀거나 시작에 실패했다" >&2; exit 1; }
# read -ra: 따옴표 없는 $KEYS 를 for 로 돌리면 글롭이 확장된다 — 도움말 키 `?` 가 한 글자 파일 이름으로 바뀐다(실측)
read -ra keys <<<"$KEYS"
for k in "${keys[@]}"; do
  T has-session -t zz-snapshot 2>/dev/null || ended
  T send-keys -t zz-snapshot "$k"
  sleep 0.3
done
[ "${#keys[@]}" -gt 0 ] && sleep 0.6

T has-session -t zz-snapshot 2>/dev/null || ended
T capture-pane -p $ANSI -t zz-snapshot
