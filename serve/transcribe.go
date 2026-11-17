package serve

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime"
	"net/http"
	"os"
	"os/exec"
	"strings"
	"syscall"
	"time"
)

// 받아쓰기 — 서버 Whisper(docs/design/serve.md «받아쓰기 — 서버 Whisper»). 폰이 녹음을 그대로 올리면 --whisper
// 명령으로 글을 만들어 돌려준다. 저절로 보내지 않는다 — 글은 폰의 입력칸에 들어가고 사람이 고친 뒤 보낸다.

const (
	// maxAudioBody 는 녹음 본문의 상한이다. 폰은 32kbps 로 녹음한다(2분 약 0.5MB). 비트레이트를 따르지 않는
	// 브라우저도 넉넉히 들어온다 — Chrome 의 기본값으로 2분이 1.9MB, 41초 AAC 음성 메모가 0.7MB 였다.
	maxAudioBody = 10 << 20
	// transcribeTimeout 은 명령 하나의 상한이다. 41초 녹음이 1.6초였다(모델 읽기 포함, RTX 5000).
	transcribeTimeout = 60 * time.Second
	// maxTranscriberOutput 은 명령의 stdout 상한이다. 2분 받아쓰기는 몇 KB 다.
	maxTranscriberOutput = 64 << 10
)

// audioTypes 는 받는 녹음 형식과 임시 파일의 확장자다. iPhone Safari 는 18.4 부터 webm(Opus), 그 전에는 mp4(AAC)로
// 녹음한다. 둘 다 CORS 의 단순 요청 형식이 아니어서 다른 출처는 사전 요청 없이 보내지 못한다(auth.go).
var audioTypes = map[string]string{"audio/webm": ".webm", "audio/mp4": ".m4a"}

// transcription 은 받아쓰기 명령의 결과다. Audio·Speech 는 명령이 주면 채우는 진단 값이고(녹음 길이와 VAD 가
// 말소리로 본 길이, 초), 받아쓰기 기록에만 쓴다.
type transcription struct {
	Text          string
	Audio, Speech float64
}

// transcriber 는 녹음 파일 하나를 글로 바꾼다. 진짜는 commandTranscriber, 핸들러 시험은 가짜를 넣는다.
type transcriber interface {
	Transcribe(ctx context.Context, audioPath, prompt string) (transcription, error)
}

// commandTranscriber 는 `<path> --prompt=<글> <오디오 파일>` 을 돌려 stdout 의 JSON 한 줄 {"text": …} 을 읽는다.
// "duration"(녹음 길이)·"speech"(VAD 가 말소리로 본 길이)는 있으면 읽는다.
// mux serve 는 Python 을 모른다 — 명령은 scripts/whisper/setup.sh 가 설치한 래퍼다. 셸을 거치지 않는다.
// 프롬프트는 `--prompt=` 한 덩어리다 — 따로 넘기면 `-` 로 시작하는 프롬프트(용어가 `--resume` 하나뿐)를 argparse 가
// 옵션으로 읽어 받아쓰기가 매번 실패한다.
type commandTranscriber struct{ path string }

func (c commandTranscriber) Transcribe(ctx context.Context, audioPath, prompt string) (transcription, error) {
	cmd := exec.CommandContext(ctx, c.path, "--prompt="+prompt, audioPath)
	// 래퍼가 exec 하지 않았거나 명령이 자식을 띄웠어도 다 죽도록 프로세스 묶음째 죽인다 — 폰이 끊은 뒤에도 GPU 를
	// 쥔 Python 이 남으면 안 된다.
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.Cancel = func() error { return syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL) }
	cmd.WaitDelay = 2 * time.Second
	stdout := &cappedBuffer{max: maxTranscriberOutput}
	stderr := &cappedBuffer{max: 16 << 10, keepTail: true}
	cmd.Stdout, cmd.Stderr = stdout, stderr
	err := cmd.Run()
	if ctxErr := ctx.Err(); ctxErr != nil {
		if errors.Is(ctxErr, context.DeadlineExceeded) {
			return transcription{}, fmt.Errorf("받아쓰기가 시간 안에 끝나지 않았다: %w", ctxErr)
		}
		return transcription{}, ctxErr
	}
	if err != nil {
		if last := lastLine(stderr.String()); last != "" {
			return transcription{}, fmt.Errorf("받아쓰기 명령 실패(%v): %s", err, last)
		}
		return transcription{}, fmt.Errorf("받아쓰기 명령 실패: %w", err)
	}
	if stdout.overflow {
		return transcription{}, fmt.Errorf("받아쓰기 명령의 출력이 %d바이트를 넘었다", maxTranscriberOutput)
	}
	var out struct {
		Text     *string `json:"text"`
		Duration float64 `json:"duration"`
		Speech   float64 `json:"speech"`
	}
	if err := json.Unmarshal([]byte(lastLine(stdout.String())), &out); err != nil || out.Text == nil {
		// 출력의 내용은 싣지 않는다 — 이 오류는 로그로 가고, 계약을 어긴 명령의 출력은 받아쓴 글일 수 있다.
		return transcription{}, fmt.Errorf("받아쓰기 명령의 출력이 {\"text\": …} 한 줄이 아니다 (%d바이트)", len(stdout.String()))
	}
	return transcription{Text: *out.Text, Audio: out.Duration, Speech: out.Speech}, nil
}

// cappedBuffer 는 max 바이트까지만 담는다. keepTail 이면 넘칠 때 앞을 버리고 끝을 남긴다(오류는 끝에 있다).
type cappedBuffer struct {
	buf      bytes.Buffer
	max      int
	keepTail bool
	overflow bool
}

func (b *cappedBuffer) Write(p []byte) (int, error) {
	n := len(p)
	if b.keepTail {
		b.buf.Write(p)
		if extra := b.buf.Len() - b.max; extra > 0 {
			b.buf.Next(extra)
			b.overflow = true
		}
		return n, nil
	}
	if room := b.max - b.buf.Len(); room < len(p) {
		p = p[:max(room, 0)]
		b.overflow = true
	}
	b.buf.Write(p)
	return n, nil
}

func (b *cappedBuffer) String() string { return b.buf.String() }

func lastLine(s string) string {
	lines := strings.Split(strings.TrimSpace(s), "\n")
	return strings.TrimSpace(lines[len(lines)-1])
}

// ── 핸들러 ───────────────────────────────────────────────────────────

type featuresView struct {
	// Dictation 은 폰이 쓸 받아쓰기다: whisper(녹음을 올린다) | browser(브라우저의 SpeechRecognition).
	Dictation string `json:"dictation"`
}

func (s *server) handleFeatures(w http.ResponseWriter, r *http.Request) {
	v := featuresView{Dictation: "browser"}
	if s.tr != nil {
		v.Dictation = "whisper"
	}
	writeJSON(w, http.StatusOK, v)
}

// transcribeView 의 result: ok | empty(말소리를 못 들었다) | busy(다른 받아쓰기가 돈다) | error.
type transcribeView struct {
	Result string `json:"result"`
	Text   string `json:"text,omitempty"`
	Detail string `json:"detail,omitempty"`
}

func (s *server) handleTranscribe(w http.ResponseWriter, r *http.Request) {
	if s.tr == nil {
		writeError(w, http.StatusNotFound, "받아쓰기가 꺼져 있습니다 (mux serve --whisper).")
		return
	}
	ct, _, _ := mime.ParseMediaType(r.Header.Get("Content-Type"))
	ext, ok := audioTypes[ct]
	if !ok { // guard 가 먼저 막는다 — 여기는 확장자를 고르는 곳이다
		writeError(w, http.StatusUnsupportedMediaType, "녹음은 audio/webm 이나 audio/mp4 로 보냅니다.")
		return
	}
	// 한 번에 한 건 — GPU 일을 겹쳐 돌리지 않는다. 녹음을 받기 전에 물어 헛되이 올리지 않게 한다.
	if !s.transcribeMu.TryLock() {
		s.log.Printf("받아쓰기 %s: busy", requestLogin(r))
		writeJSON(w, http.StatusOK, transcribeView{Result: "busy"})
		return
	}
	defer s.transcribeMu.Unlock()
	// 서버 전체의 ReadTimeout(15초)·WriteTimeout(60초)은 JSON 요청에 맞춘 것이다. 느린 망의 녹음 올리기와 받아쓰기
	// 시간을 이 요청에만 더 준다. httptest 처럼 지원하지 않는 곳에서는 그대로 둔다.
	rc := http.NewResponseController(w)
	_ = rc.SetReadDeadline(time.Now().Add(transcribeTimeout))
	_ = rc.SetWriteDeadline(time.Now().Add(2*transcribeTimeout + 10*time.Second))

	path, size, status, err := saveRecording(w, r, ext)
	if path != "" {
		defer os.Remove(path)
	}
	if err != nil {
		s.log.Printf("받아쓰기 %s: 녹음을 받지 못했다 (%d): %v", requestLogin(r), status, err)
		writeError(w, status, err.Error())
		return
	}

	d, derr := readDictation(s.cfg.DictationFile)
	if derr != nil {
		// 용어 파일이 망가졌다고 받아쓰기를 막지 않는다 — 용어 없이 받아쓴다.
		s.log.Printf("받아쓰기 용어 파일을 읽지 못했다: %v", derr)
	}
	prompt := dictationPrompt(s.sessionNames(), d.terms)

	ctx, cancel := context.WithTimeout(r.Context(), transcribeTimeout)
	defer cancel()
	start := time.Now()
	got, err := s.tr.Transcribe(ctx, path, prompt)
	took := time.Since(start).Seconds()
	entry := dictationEntry{At: start.Format(time.RFC3339), Login: requestLogin(r), Type: ct, Bytes: size, Took: round2(took)}
	// 서버 로그(journald)에는 누가·크기·시간·갈래만 — 글은 받아쓰기 기록 파일(0600)에만 남긴다(recordDictation).
	switch {
	case err != nil:
		s.log.Printf("받아쓰기 %s %dKB %.1f초: error: %v", requestLogin(r), size>>10, took, err)
		entry.Result, entry.Error = "error", err.Error()
		writeJSON(w, http.StatusOK, transcribeView{Result: "error", Detail: err.Error()})
	default:
		heard := strings.TrimSpace(got.Text)
		text := strings.TrimSpace(d.fix(heard))
		result := "ok"
		if text == "" {
			result = "empty"
		}
		s.log.Printf("받아쓰기 %s %dKB %.1f초: %s (%d자)", requestLogin(r), size>>10, took, result, len([]rune(text)))
		entry.Result, entry.Heard, entry.Text = result, heard, text
		entry.Audio, entry.Speech = round2(got.Audio), round2(got.Speech)
		writeJSON(w, http.StatusOK, transcribeView{Result: result, Text: text})
	}
	s.recordDictation(entry)
}

// recordDictation 은 받아쓰기 기록에 한 줄을 붙인다. 경로가 비면(제로값) 쓰지 않는다. 못 써도 받아쓰기는 이미
// 폰에 갔다 — 서버 로그에 이유만 남긴다(글은 싣지 않는다).
func (s *server) recordDictation(e dictationEntry) {
	if s.cfg.DictationLog == "" {
		return
	}
	if err := appendDictationLog(s.cfg.DictationLog, e); err != nil {
		s.log.Printf("받아쓰기 기록을 쓰지 못했다: %v", err)
	}
}

// saveRecording 은 본문을 0600 임시 파일에 쓴다. 파일을 만들었으면 path 를 돌려준다(오류여도 — 지우는 것은 부른 쪽).
func saveRecording(w http.ResponseWriter, r *http.Request, ext string) (path string, size int64, status int, err error) {
	f, err := os.CreateTemp("", "mux-dictation-*"+ext) // CreateTemp 는 0600 으로 만든다
	if err != nil {
		return "", 0, http.StatusInternalServerError, fmt.Errorf("임시 파일을 만들지 못했습니다: %w", err)
	}
	path = f.Name()
	size, err = io.Copy(f, http.MaxBytesReader(w, r.Body, maxAudioBody))
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	var tooBig *http.MaxBytesError
	switch {
	case errors.As(err, &tooBig):
		return path, size, http.StatusRequestEntityTooLarge, fmt.Errorf("녹음이 %dMB 를 넘었습니다", maxAudioBody>>20)
	case err != nil:
		return path, size, http.StatusBadRequest, fmt.Errorf("녹음을 받지 못했습니다: %w", err)
	case size == 0:
		return path, 0, http.StatusBadRequest, errors.New("빈 녹음입니다")
	}
	return path, size, 0, nil
}

// sessionNames 는 프롬프트에 넣을 지금 세션 이름이다. tmux 를 못 읽어도 받아쓰기는 한다 — 이름 없이.
func (s *server) sessionNames() []string {
	s.listMu.Lock()
	sessions, err := s.b.Sessions()
	s.listMu.Unlock()
	if err != nil {
		return nil
	}
	names := make([]string, 0, len(sessions))
	for _, ss := range sessions {
		names = append(names, ss.Name)
	}
	return names
}
