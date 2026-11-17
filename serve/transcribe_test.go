package serve

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"
)

// TestMain 은 테스트 바이너리를 가짜 받아쓰기 명령으로도 쓴다 — commandTranscriber 가 진짜 exec·인자·stdout·
// 프로세스 죽이기를 거치게. MUX_FAKE_WHISPER 가 있으면 테스트를 돌리지 않고 그 흉내만 낸다.
func TestMain(m *testing.M) {
	if mode := os.Getenv("MUX_FAKE_WHISPER"); mode != "" {
		os.Exit(fakeWhisper(mode, os.Args[1:]))
	}
	os.Exit(m.Run())
}

// fakeWhisper 는 `<명령> --prompt=<글> <파일>` 계약을 흉내 낸다.
func fakeWhisper(mode string, args []string) int {
	prompt, ok := strings.CutPrefix(firstOr(args, ""), "--prompt=")
	if len(args) != 2 || !ok {
		fmt.Fprintf(os.Stderr, "인자가 계약과 다르다: %q\n", args)
		return 2
	}
	file := args[1]
	switch mode {
	case "echo":
		data, err := os.ReadFile(file)
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			return 1
		}
		json.NewEncoder(os.Stdout).Encode(map[string]string{"text": prompt + "|" + string(data)})
	case "fail":
		fmt.Fprintln(os.Stderr, "Traceback (most recent call last):")
		fmt.Fprintln(os.Stderr, "RuntimeError: CUDA failed with error no CUDA-capable device is detected")
		return 3
	case "garbage":
		// 계약을 어긴 명령 — JSON 이 아닌 맨 글(받아쓴 글일 수 있다)을 낸다.
		fmt.Println("비밀 프로젝트 이름을 맨 글로")
	case "huge":
		os.Stdout.WriteString(`{"text": "` + strings.Repeat("가", maxTranscriberOutput) + `"}` + "\n")
	case "hang":
		// 자식 하나를 띄우고 기다린다 — 죽일 때 자식까지 죽는지 본다(파이썬 래퍼가 exec 하지 않은 경우).
		child, err := os.StartProcess("/bin/sleep", []string{"sleep", "30"}, &os.ProcAttr{})
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			return 1
		}
		os.WriteFile(os.Getenv("MUX_FAKE_WHISPER_PIDFILE"), []byte(strconv.Itoa(child.Pid)), 0o600)
		child.Wait()
	default:
		fmt.Fprintln(os.Stderr, "모르는 흉내:", mode)
		return 2
	}
	return 0
}

// fakeTranscriber 는 핸들러 시험용이다. 받은 파일의 내용·권한과 프롬프트를 적어 둔다.
type fakeTranscriber struct {
	text    string
	err     error
	block   chan struct{} // 있으면 닫힐 때까지(또는 ctx 가 끝날 때까지) 기다린다
	started chan struct{}

	mu      sync.Mutex
	path    string
	audio   string
	perm    os.FileMode
	prompt  string
	ctxErr  error
	calls   int
	stillOn bool // 돌아올 때 파일이 아직 있었나
}

func (f *fakeTranscriber) Transcribe(ctx context.Context, path, prompt string) (string, error) {
	data, _ := os.ReadFile(path)
	st, _ := os.Stat(path)
	f.mu.Lock()
	f.calls++
	f.path, f.audio, f.prompt = path, string(data), prompt
	if st != nil {
		f.perm = st.Mode().Perm()
	}
	f.mu.Unlock()
	if f.started != nil {
		close(f.started)
	}
	if f.block != nil {
		select {
		case <-f.block:
		case <-ctx.Done():
			f.mu.Lock()
			f.ctxErr = ctx.Err()
			f.mu.Unlock()
			return "", ctx.Err()
		}
	}
	return f.text, f.err
}

func whisperServer(t *testing.T, b backend, tr transcriber, dictationFile string, logOut io.Writer) http.Handler {
	t.Helper()
	cfg, err := Config{Addr: "127.0.0.1:7690", Allow: []string{testLogin}, DictationFile: dictationFile}.normalized()
	if err != nil {
		t.Fatal(err)
	}
	if logOut == nil {
		logOut = io.Discard
	}
	return newHandlerWith(cfg, b, tr, log.New(logOut, "", 0))
}

// audioPost 는 폰의 녹음 올리기를 흉내 낸다 — 본문은 녹음 그대로, Content-Type 은 녹음 형식.
func audioPost(body, contentType string) *http.Request {
	return request(http.MethodPost, "/api/transcribe", body, func(r *http.Request) {
		r.Header.Set("Content-Type", contentType)
	})
}

func TestFeaturesSaysWhichDictation(t *testing.T) {
	off := decode[featuresView](t, do(testServer(t, newFake()), request("GET", "/api/features", "", nil)))
	if off.Dictation != "browser" {
		t.Errorf("--whisper 없이 %q, want browser", off.Dictation)
	}
	on := decode[featuresView](t, do(whisperServer(t, newFake(), &fakeTranscriber{}, "", nil), request("GET", "/api/features", "", nil)))
	if on.Dictation != "whisper" {
		t.Errorf("--whisper 와 함께 %q, want whisper", on.Dictation)
	}
}

func TestTranscribeIsOffWithoutWhisper(t *testing.T) {
	if w := do(testServer(t, newFake()), audioPost("ogg", "audio/webm")); w.Code != http.StatusNotFound {
		t.Errorf("--whisper 없이 %d, want 404", w.Code)
	}
}

// 녹음 본문은 이 경로에서만, audio/webm·audio/mp4 로만 받는다. 둘 다 CORS 의 단순 요청 형식이 아니라 다른 출처는
// 사전 요청 없이 보내지 못한다. multipart/form-data·text/plain 은 단순 요청이라 받으면 안 된다.
func TestTranscribeAcceptsOnlyAudioBodies(t *testing.T) {
	h := whisperServer(t, newFake(), &fakeTranscriber{text: "안녕"}, "", nil)
	cases := []struct {
		ct   string
		want int
	}{
		{"audio/webm;codecs=opus", http.StatusOK},
		{"audio/webm", http.StatusOK},
		{"audio/mp4", http.StatusOK},
		{"multipart/form-data; boundary=x", http.StatusUnsupportedMediaType},
		{"text/plain", http.StatusUnsupportedMediaType},
		{"application/x-www-form-urlencoded", http.StatusUnsupportedMediaType},
		{"application/json", http.StatusUnsupportedMediaType},
		{"audio/ogg", http.StatusUnsupportedMediaType},
		{"", http.StatusUnsupportedMediaType},
	}
	for _, c := range cases {
		if w := do(h, audioPost("rec", c.ct)); w.Code != c.want {
			t.Errorf("Content-Type %q: %d, want %d", c.ct, w.Code, c.want)
		}
	}
	// 다른 경로는 그대로 JSON 만 — 녹음 형식도, 단순 요청 형식도 받지 않는다.
	for _, path := range []string{"/api/prompt", "/api/answer"} {
		for _, ct := range []string{"audio/webm", "multipart/form-data; boundary=x", "text/plain", "application/x-www-form-urlencoded"} {
			w := do(h, request(http.MethodPost, path, "{}", func(r *http.Request) { r.Header.Set("Content-Type", ct) }))
			if w.Code != http.StatusUnsupportedMediaType {
				t.Errorf("%s 에 %s: %d, want 415", path, ct, w.Code)
			}
		}
	}
	w := do(h, request(http.MethodPost, "/api/transcribe", "rec", func(r *http.Request) {
		r.Header.Set("Content-Type", "audio/webm")
		r.Header.Set("Origin", "https://evil.example")
	}))
	if w.Code != http.StatusForbidden {
		t.Errorf("다른 Origin: %d, want 403", w.Code)
	}
}

func TestTranscribeResults(t *testing.T) {
	cases := []struct {
		name string
		tr   *fakeTranscriber
		want transcribeView
	}{
		{"글", &fakeTranscriber{text: "  mux 패널에서 tmux 세션을 열고\n"}, transcribeView{Result: "ok", Text: "mux 패널에서 tmux 세션을 열고"}},
		{"말소리 없음", &fakeTranscriber{text: " "}, transcribeView{Result: "empty"}},
		{"명령 실패", &fakeTranscriber{err: errors.New("받아쓰기 명령 실패: RuntimeError: CUDA failed")},
			transcribeView{Result: "error", Detail: "받아쓰기 명령 실패: RuntimeError: CUDA failed"}},
	}
	for _, c := range cases {
		w := do(whisperServer(t, newFake(), c.tr, "", nil), audioPost("rec", "audio/webm"))
		if w.Code != http.StatusOK {
			t.Errorf("%s: %d — 결과는 늘 200 에 갈래로", c.name, w.Code)
			continue
		}
		if got := decode[transcribeView](t, w); got != c.want {
			t.Errorf("%s: %+v, want %+v", c.name, got, c.want)
		}
	}
}

func TestTranscribeHandsTheRecordingOverPrivatelyAndRemovesIt(t *testing.T) {
	tr := &fakeTranscriber{text: "안녕"}
	do(whisperServer(t, newFake(), tr, "", nil), audioPost("\x1aE\xdf\xa3 webm bytes", "audio/webm;codecs=opus"))
	if tr.audio != "\x1aE\xdf\xa3 webm bytes" {
		t.Errorf("받은 녹음 %q — 본문 그대로여야 한다", tr.audio)
	}
	if tr.perm != 0o600 {
		t.Errorf("임시 파일 권한 %o, want 600", tr.perm)
	}
	if filepath.Ext(tr.path) != ".webm" {
		t.Errorf("임시 파일 %q — 녹음 형식의 확장자", tr.path)
	}
	if _, err := os.Stat(tr.path); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("받아쓰기 뒤에도 녹음 파일이 남았다: %v", err)
	}
	mp4 := &fakeTranscriber{text: "안녕"}
	do(whisperServer(t, newFake(), mp4, "", nil), audioPost("ftyp", "audio/mp4"))
	if filepath.Ext(mp4.path) != ".m4a" {
		t.Errorf("audio/mp4 의 임시 파일 %q", mp4.path)
	}
}

func TestTranscribePromptsWithSessionsAndTermsAndFixesTheText(t *testing.T) {
	file := filepath.Join(t.TempDir(), "dictation.txt")
	os.WriteFile(file, []byte("herdr\ngolangci-lint\n고랑 쉐이 린트 => golangci-lint\n허드 => herdr\n"), 0o600)
	tr := &fakeTranscriber{text: "허드 에서 고랑 쉐이 린트 돌려"}
	w := do(whisperServer(t, newFake(), tr, file, nil), audioPost("rec", "audio/webm"))
	if got := decode[transcribeView](t, w); got.Text != "herdr 에서 golangci-lint 돌려" {
		t.Errorf("바꿔 쓰기: %q", got.Text)
	}
	if want := "mux, notes, herdr, golangci-lint."; tr.prompt != want {
		t.Errorf("프롬프트 %q, want %q", tr.prompt, want)
	}
	// 파일은 요청마다 읽는다 — 고친 뒤 다시 띄울 필요가 없다.
	os.WriteFile(file, []byte("허드 => herdr!\n"), 0o600)
	w = do(whisperServer(t, newFake(), &fakeTranscriber{text: "허드"}, file, nil), audioPost("rec", "audio/webm"))
	if got := decode[transcribeView](t, w); got.Text != "herdr!" {
		t.Errorf("고친 파일: %q", got.Text)
	}
}

func TestTranscribeStillWorksWithoutSessions(t *testing.T) {
	b := newFake()
	b.sessionsErr = errors.New("no server running")
	tr := &fakeTranscriber{text: "안녕"}
	w := do(whisperServer(t, b, tr, "", nil), audioPost("rec", "audio/webm"))
	if got := decode[transcribeView](t, w); got.Result != "ok" || tr.prompt != "" {
		t.Errorf("tmux 없이도 받아쓴다: %+v, 프롬프트 %q", got, tr.prompt)
	}
}

func TestTranscribeOneAtATime(t *testing.T) {
	tr := &fakeTranscriber{text: "안녕", block: make(chan struct{}), started: make(chan struct{})}
	h := whisperServer(t, newFake(), tr, "", nil)
	first := make(chan transcribeView)
	go func() { first <- decode[transcribeView](t, do(h, audioPost("rec", "audio/webm"))) }()
	<-tr.started
	if got := decode[transcribeView](t, do(h, audioPost("rec", "audio/webm"))); got.Result != "busy" {
		t.Errorf("겹친 요청: %+v, want busy", got)
	}
	close(tr.block)
	if got := <-first; got.Result != "ok" {
		t.Errorf("먼저 온 요청: %+v", got)
	}
	if tr.calls != 1 {
		t.Errorf("명령을 %d번 불렀다, want 1", tr.calls)
	}
}

func TestTranscribeRefusesBadBodies(t *testing.T) {
	tr := &fakeTranscriber{text: "안녕"}
	h := whisperServer(t, newFake(), tr, "", nil)
	if w := do(h, audioPost("", "audio/webm")); w.Code != http.StatusBadRequest {
		t.Errorf("빈 녹음: %d, want 400", w.Code)
	}
	if w := do(h, audioPost(strings.Repeat("x", maxAudioBody+1), "audio/webm")); w.Code != http.StatusRequestEntityTooLarge {
		t.Errorf("상한 넘는 녹음: %d, want 413", w.Code)
	}
	if tr.calls != 0 {
		t.Errorf("받지 않은 녹음으로 명령을 %d번 불렀다", tr.calls)
	}
}

// 폰이 끊으면(잠김·망 바뀜·보내기를 눌러 거둠) 명령도 멈춘다 — 아무도 기다리지 않는 GPU 일을 하지 않는다.
func TestTranscribeStopsWhenThePhoneLeaves(t *testing.T) {
	tr := &fakeTranscriber{block: make(chan struct{}), started: make(chan struct{})}
	h := whisperServer(t, newFake(), tr, "", nil)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		do(h, audioPost("rec", "audio/webm").WithContext(ctx))
		close(done)
	}()
	<-tr.started
	cancel()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("폰이 끊은 뒤에도 받아쓰기가 끝나지 않았다")
	}
	if !errors.Is(tr.ctxErr, context.Canceled) {
		t.Errorf("명령의 context: %v, want canceled", tr.ctxErr)
	}
	if _, err := os.Stat(tr.path); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("끊긴 뒤 녹음 파일이 남았다: %v", err)
	}
}

func TestTranscribeLogNeverCarriesTheText(t *testing.T) {
	var logs bytes.Buffer
	tr := &fakeTranscriber{text: "비밀 프로젝트 이름"}
	do(whisperServer(t, newFake(), tr, "", &logs), audioPost("rec", "audio/webm"))
	if strings.Contains(logs.String(), "비밀") {
		t.Errorf("로그에 받아쓴 글이 남았다: %s", logs.String())
	}
	if !strings.Contains(logs.String(), "받아쓰기") || !strings.Contains(logs.String(), testLogin) {
		t.Errorf("로그에 누가·결과가 없다: %s", logs.String())
	}
}

// ── 명령 ─────────────────────────────────────────────────────────────

func fakeCommand(t *testing.T, mode string) commandTranscriber {
	t.Helper()
	t.Setenv("MUX_FAKE_WHISPER", mode)
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	return commandTranscriber{path: exe}
}

func audioFile(t *testing.T, content string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "rec.webm")
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestCommandTranscriberFollowsTheContract(t *testing.T) {
	for _, prompt := range []string{
		"mux; $(rm -rf ~).", // 셸을 거치지 않으니 $(…)·; 은 그냥 글자다
		"--resume.",         // - 로 시작해도 옵션이 되지 않는다(--prompt= 한 덩어리)
		"",
	} {
		text, err := fakeCommand(t, "echo").Transcribe(context.Background(), audioFile(t, "녹음"), prompt)
		if err != nil || text != prompt+"|녹음" {
			t.Errorf("프롬프트 %q: Transcribe = %q, %v", prompt, text, err)
		}
	}
}

func TestCommandTranscriberReportsFailures(t *testing.T) {
	cases := []struct{ mode, want string }{
		{"fail", "CUDA failed with error no CUDA-capable device"}, // stderr 의 마지막 줄
		{"garbage", "받아쓰기 명령의 출력"},
		{"huge", "받아쓰기 명령의 출력"},
	}
	for _, c := range cases {
		_, err := fakeCommand(t, c.mode).Transcribe(context.Background(), audioFile(t, "녹음"), "")
		if err == nil || !strings.Contains(err.Error(), c.want) {
			t.Errorf("%s: %v, want %q 를 담은 오류", c.mode, err, c.want)
		}
		// 이 오류는 로그로 간다 — 계약을 어긴 명령이 낸 글(받아쓴 글일 수 있다)을 싣지 않는다.
		if err != nil && strings.Contains(err.Error(), "비밀") {
			t.Errorf("%s: 오류에 명령의 출력이 실렸다: %v", c.mode, err)
		}
	}
}

func TestCommandTranscriberKillsTheWholeGroup(t *testing.T) {
	pidFile := filepath.Join(t.TempDir(), "child.pid")
	t.Setenv("MUX_FAKE_WHISPER_PIDFILE", pidFile)
	ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
	defer cancel()
	start := time.Now()
	_, err := fakeCommand(t, "hang").Transcribe(ctx, audioFile(t, "녹음"), "")
	if err == nil || time.Since(start) > 5*time.Second {
		t.Fatalf("시간이 지나도 돌아오지 않았다: %v (%s)", err, time.Since(start))
	}
	data, rerr := os.ReadFile(pidFile)
	if rerr != nil {
		t.Fatalf("가짜 명령이 자식을 띄우지 못했다: %v", rerr)
	}
	pid, _ := strconv.Atoi(string(data))
	deadline := time.Now().Add(3 * time.Second)
	for syscall.Kill(pid, 0) == nil {
		if time.Now().After(deadline) {
			syscall.Kill(pid, syscall.SIGKILL)
			t.Fatalf("명령을 죽인 뒤에도 자식 %d 가 살아 있다 — 프로세스 묶음째 죽여야 한다", pid)
		}
		time.Sleep(50 * time.Millisecond)
	}
}

func firstOr(s []string, def string) string {
	if len(s) == 0 {
		return def
	}
	return s[0]
}
