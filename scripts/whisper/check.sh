#!/usr/bin/env bash
# 설치한 받아쓰기 명령을 진짜 모델로 확인한다(docs/design/serve.md «받아쓰기 — 서버 Whisper»).
#
#   scripts/whisper/check.sh [녹음 파일…]
#
# 폰이 올리는 두 형식(webm·Opus, m4a·AAC)의 무음과 잡음을 받아써 빈 글이 나오는지 본다 — 읽기가 되고, VAD 가 켜져
# 있어 «다음 영상에서 만나요.» 같은 지어낸 말이 나오지 않는다는 뜻이다. 녹음 파일을 주면 그 글과 걸린 시간도 보인다.
# 종료 코드: 0 전부 통과, 1 하나라도 실패.
set -uo pipefail

T=${MUX_WHISPER:-${MUX_WHISPER_HOME:-$HOME/.local/opt/mux-whisper}/transcribe}
[ -x "$T" ] || { echo "받아쓰기 명령이 없다: $T (scripts/whisper/setup.sh)" >&2; exit 1; }
command -v ffmpeg >/dev/null || { echo "ffmpeg 가 필요하다(시험 소리를 만든다)" >&2; exit 1; }
DIR=$(mktemp -d)
trap 'rm -rf "$DIR"' EXIT

ffmpeg -loglevel error -f lavfi -i anullsrc=r=48000:cl=mono -t 8 -c:a libopus "$DIR/silence.webm"
ffmpeg -loglevel error -f lavfi -i anullsrc=r=48000:cl=mono -t 8 -c:a aac "$DIR/silence.m4a"
ffmpeg -loglevel error -f lavfi -i "anoisesrc=r=48000:a=0.02:c=pink" -t 8 -c:a libopus "$DIR/noise.webm"

pass=0 total=0
for f in "$DIR"/silence.webm "$DIR"/silence.m4a "$DIR"/noise.webm; do
  total=$((total + 1))
  out=$("$T" --prompt="mux, tmux." "$f" 2>"$DIR/err")
  code=$?
  # text 가 빈 글인가 — duration·speech 같은 다른 값은 보지 않는다. JSON 이 아니면 실패다.
  if [ $code -eq 0 ] && printf '%s' "$out" | python3 -I -c 'import json, sys; sys.exit(json.load(sys.stdin)["text"] != "")' 2>/dev/null; then
    pass=$((pass + 1))
    echo "통과  $(basename "$f") → 빈 글"
  else
    echo "실패  $(basename "$f") → 종료 $code, $out $(tail -1 "$DIR/err")"
  fi
done

for f in "$@"; do
  start=$(date +%s%N)
  out=$("$T" --prompt="mux, tmux, Claude." "$f" 2>"$DIR/err") || out="실패: $(tail -1 "$DIR/err")"
  ms=$((($(date +%s%N) - start) / 1000000))
  printf '%s (%d.%d초) %s\n' "$(basename "$f")" $((ms / 1000)) $((ms % 1000 / 100)) "$out"
done

echo "$pass/$total 통과"
[ "$pass" -eq "$total" ] && [ "$total" -eq 3 ]
