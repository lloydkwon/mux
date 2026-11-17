#!/usr/bin/env bash
# mux serve 의 받아쓰기 명령을 설치한다(docs/design/serve.md «받아쓰기 — 서버 Whisper»).
#
#   scripts/whisper/setup.sh            # → ~/.local/opt/mux-whisper/transcribe
#   mux serve --allow … --whisper ~/.local/opt/mux-whisper/transcribe
#
# uv 가상환경에 고정 버전(requirements.txt)을 넣고, CUDA 라이브러리 경로를 거는 래퍼 `transcribe` 를 만들고, 모델을
# 미리 받아 둔다 — 첫 받아쓰기가 1.6GB 내려받기를 기다리지 않게. 다시 돌려도 된다. MUX_WHISPER_HOME 으로 자리를 바꾼다.
set -euo pipefail

HERE=$(cd "$(dirname "$0")" && pwd)
DEST=${MUX_WHISPER_HOME:-$HOME/.local/opt/mux-whisper}
VENV=$DEST/venv
# 고정한 버전들이 끌어오는 의존성도 이 날짜에 멈춘다 — 다시 설치해도 같은 묶음이 깔린다.
EXCLUDE_NEWER=2026-11-03

command -v uv >/dev/null || { echo "uv 가 필요하다 — https://docs.astral.sh/uv/" >&2; exit 1; }
command -v nvidia-smi >/dev/null || echo "주의: nvidia-smi 가 없다 — 받아쓰기는 CUDA 를 쓴다(transcribe.py)." >&2

mkdir -p "$DEST"
[ -x "$VENV/bin/python" ] || uv venv --quiet --python 3.12 "$VENV"
VIRTUAL_ENV=$VENV uv pip install --quiet --exclude-newer "$EXCLUDE_NEWER" -r "$HERE/requirements.txt"
install -m 644 "$HERE/transcribe.py" "$DEST/transcribe.py"

SITE=$("$VENV/bin/python" -I -c 'import sysconfig; print(sysconfig.get_paths()["purelib"])')
# 아래 래퍼는 경로를 큰따옴표 안에 그대로 적는다 — 그 안에서 뜻이 바뀌는 글자가 든 경로는 받지 않는다.
case "$DEST$SITE" in
  *[\"\$\`\\]*) echo "설치 경로에 \" \$ \` \\ 를 쓸 수 없다: $DEST" >&2; exit 1 ;;
esac
# 래퍼는 exec 로 Python 이 된다 — mux serve 가 죽일 때 셸만 죽고 GPU 를 쥔 Python 이 남지 않게(그래도 serve 는
# 프로세스 묶음째 죽인다).
cat >"$DEST/transcribe.new" <<WRAPPER
#!/bin/sh
# mux serve --whisper 가 부르는 받아쓰기 명령. scripts/whisper/setup.sh 가 만들었다 — 고치지 말고 setup.sh 를 다시 돌린다.
export LD_LIBRARY_PATH="$SITE/nvidia/cublas/lib:$SITE/nvidia/cudnn/lib\${LD_LIBRARY_PATH:+:\$LD_LIBRARY_PATH}"
exec "$VENV/bin/python" -I "$DEST/transcribe.py" "\$@"
WRAPPER
chmod 755 "$DEST/transcribe.new"
mv -f "$DEST/transcribe.new" "$DEST/transcribe"

echo "모델을 받는다(처음이면 1.6GB)…"
"$VENV/bin/python" -I -c 'from faster_whisper import download_model; download_model("large-v3-turbo")' >/dev/null

echo "설치했다: $DEST/transcribe"
echo "확인: $HERE/check.sh   (무음을 받아써 빈 글이 나오는지 — VAD 가드)"
