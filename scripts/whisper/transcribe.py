"""mux serve 의 받아쓰기 명령(docs/design/serve.md «받아쓰기 — 서버 Whisper»).

쓰임: transcribe --prompt=<용어 목록> <오디오 파일>
stdout 에 JSON 한 줄 {"text": "..."} 만 쓴다. 받아쓴 글은 stdout 에만 나간다 — mux serve 는 stderr 의 마지막 줄을
오류로 로그에 남긴다. 실패는 0 이 아닌 종료 코드다. 프롬프트는 `--prompt=` 한 덩어리로 받는다 — `-` 로 시작하는
용어 하나뿐이면 `--prompt <글>` 은 옵션으로 읽혀 실패한다.

아래 설정은 2026-11-17 에 같은 녹음으로 견줘 정한 것이다. 바꾸기 전에 문서의 «되돌리면 안 되는 설정» 을 읽는다.
"""

import argparse
import json
import sys

MODEL = "large-v3-turbo"
LANGUAGE = "ko"


def main() -> int:
    p = argparse.ArgumentParser(description="녹음 하나를 받아써 {\"text\": …} 를 낸다")
    p.add_argument("--prompt", default="", help="용어 목록 프롬프트 (mux serve 가 만든다)")
    p.add_argument("audio", help="오디오 파일 (webm·m4a — PyAV 가 바로 읽는다)")
    a = p.parse_args()

    from faster_whisper import BatchedInferencePipeline, WhisperModel  # --help 를 빨리 내려고 여기서 읽는다

    # local_files_only: 모델은 setup.sh 가 받아 두었다. 받아쓸 때마다 Hugging Face 에 묻지 않는다.
    # CUDA 를 못 쓰면 여기서 실패한다 — CPU 로 몰래 물러서지 않는다(몇십 초가 걸리는데 폰은 이유를 모른다).
    model = WhisperModel(MODEL, device="cuda", compute_type="float16", local_files_only=True)
    # 묶음 처리: VAD 로 자른 30초 이하 조각마다 프롬프트를 새로 붙인다. 순차 model.transcribe 는 이어쓰기를 끄면
    # 프롬프트가 첫 30초 창에만 걸려, 82초 녹음의 뒤 절반에서 mux 가 «묵스», PyCharm 이 «파이참» 으로 돌아갔다.
    segments, _ = BatchedInferencePipeline(model).transcribe(
        a.audio,
        language=LANGUAGE,
        beam_size=5,
        batch_size=8,
        initial_prompt=a.prompt or None,
        # 끄면 8초 무음·잡음에서 «다음 영상에서 만나요.» 를 지어냈다.
        vad_filter=True,
        # 켜면 프롬프트와 함께 마지막 문장을 여섯 번 되풀이하거나 엉뚱한 영어를 냈다(순차 처리에서 잼). 묶음 처리는
        # 어차피 끄지만, 뜻을 남긴다.
        condition_on_previous_text=False,
        # hotwords 는 쓰지 않는다 — 뒷부분이 끊겨 사라졌다.
    )
    text = " ".join(s.text.strip() for s in segments).strip()
    sys.stdout.write(json.dumps({"text": text}, ensure_ascii=False) + "\n")
    return 0


if __name__ == "__main__":
    sys.exit(main())
