package serve

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"math"
	"os"
	"path/filepath"
)

// 받아쓰기 기록(기본 ~/.config/mux/dictation-log.jsonl, docs/design/serve.md «받아쓰기 — 서버 Whisper»).
// 받아쓸 때마다 JSON 한 줄을 붙인다 — 나중에 무엇을 어떻게 받아썼는지 보려고(사용자 결정, 2026-11-17). 녹음은
// 남기지 않는다. 서버 로그(journald)에는 지금처럼 글이 없다 — 글은 이 파일에만, 0600 으로.

// maxDictationLog 를 넘으면 기록을 `.1` 로 넘기고 새로 쓴다. 넘긴 것은 하나만 남는다. 한 줄이 300바이트 안팎이라
// 4MB 면 만 번이 넘는다.
const maxDictationLog = 4 << 20

// dictationEntry 는 받아쓰기 한 번이다.
type dictationEntry struct {
	At    string  `json:"at"` // 받아쓰기를 시작한 때(RFC 3339)
	Login string  `json:"login"`
	Type  string  `json:"type"`  // 녹음 형식: audio/webm | audio/mp4
	Bytes int64   `json:"bytes"` // 녹음 크기
	Took  float64 `json:"took"`  // 받아쓰기에 걸린 초(명령을 띄우고 모델을 읽는 시간 포함)
	// Audio·Speech 는 명령이 주는 진단 값이다 — 녹음 길이와 VAD 가 말소리로 본 길이(초). 둘의 차이가 크면 VAD 가
	// 말을 잘라냈을 수 있다.
	Audio  float64 `json:"audio,omitempty"`
	Speech float64 `json:"speech,omitempty"`
	Result string  `json:"result"`          // ok | empty | error
	Heard  string  `json:"heard,omitempty"` // Whisper 가 낸 그대로
	Text   string  `json:"text,omitempty"`  // 바꿔 쓰기 뒤 — 폰의 입력칸에 간 글
	Error  string  `json:"error,omitempty"`
}

// appendDictationLog 는 기록에 한 줄을 붙인다. 받아쓰기 요청은 한 번에 하나라(transcribeMu) 줄이 섞이지 않는다.
func appendDictationLog(path string, e dictationEntry) error {
	line, err := json.Marshal(e)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	if st, err := os.Stat(path); err == nil && st.Size() >= maxDictationLog {
		if err := os.Rename(path, path+".1"); err != nil {
			return fmt.Errorf("기록을 넘기지 못했다: %w", err)
		}
	} else if err != nil && !errors.Is(err, fs.ErrNotExist) {
		return err
	}
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_APPEND|os.O_CREATE, 0o600)
	if err != nil {
		return err
	}
	_, werr := f.Write(append(line, '\n'))
	if cerr := f.Close(); werr == nil {
		werr = cerr
	}
	return werr
}

// round2 는 초를 소수 둘째 자리로 줄인다 — 기록을 눈으로 읽기 좋게.
func round2(x float64) float64 { return math.Round(x*100) / 100 }
