package serve

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"io"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// logServer 는 받아쓰기 기록(DictationLog)을 켠 서버다.
func logServer(t *testing.T, tr transcriber, dictationFile, logPath string) http.Handler {
	t.Helper()
	cfg, err := Config{Addr: "127.0.0.1:7690", Allow: []string{testLogin}, DictationFile: dictationFile, DictationLog: logPath}.normalized()
	if err != nil {
		t.Fatal(err)
	}
	return newHandlerWith(cfg, newFake(), tr, log.New(io.Discard, "", 0))
}

func readDictationLog(t *testing.T, path string) []dictationEntry {
	t.Helper()
	f, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	var out []dictationEntry
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		var e dictationEntry
		if err := json.Unmarshal(sc.Bytes(), &e); err != nil {
			t.Fatalf("기록 줄이 JSON 이 아니다: %q: %v", sc.Text(), err)
		}
		out = append(out, e)
	}
	return out
}

// 받아쓴 글은 기록 파일에 남는다(사용자 결정, 2026-11-17 — 나중에 무엇이 이상했는지 보려고). 녹음은 남기지 않는다.
func TestDictationLogKeepsWhatWasHeardAndWhatThePhoneGot(t *testing.T) {
	dir := t.TempDir()
	fixes := filepath.Join(dir, "dictation.txt")
	os.WriteFile(fixes, []byte("허드 => herdr\n"), 0o600)
	logPath := filepath.Join(dir, "dictation-log.jsonl")

	do(logServer(t, &fakeTranscriber{text: " 허드 돌려 ", seconds: 8.456, speech: 6.2}, fixes, logPath), audioPost("rec", "audio/webm;codecs=opus"))
	do(logServer(t, &fakeTranscriber{text: ""}, fixes, logPath), audioPost("rec2", "audio/mp4"))

	got := readDictationLog(t, logPath)
	if len(got) != 2 {
		t.Fatalf("기록 %d줄, want 2: %+v", len(got), got)
	}
	ok := got[0]
	if ok.Result != "ok" || ok.Heard != "허드 돌려" || ok.Text != "herdr 돌려" {
		t.Errorf("ok 줄: %+v — Whisper 가 낸 그대로와 바꿔 쓴 뒤 폰에 간 글을 둘 다", ok)
	}
	if ok.Type != "audio/webm" || ok.Bytes != 3 || ok.Audio != 8.46 || ok.Speech != 6.2 || ok.Login != testLogin || ok.At == "" {
		t.Errorf("ok 줄의 진단 값: %+v", ok)
	}
	if e := got[1]; e.Result != "empty" || e.Type != "audio/mp4" || e.Text != "" {
		t.Errorf("empty 줄: %+v", e)
	}
	if st, _ := os.Stat(logPath); st.Mode().Perm() != 0o600 {
		t.Errorf("기록 파일 권한 %o, want 600 — 받아쓴 글이다", st.Mode().Perm())
	}
	// 녹음 자체는 어디에도 남지 않는다 — 기록 폴더에는 기록 파일과 용어 파일뿐.
	entries, _ := os.ReadDir(dir)
	if len(entries) != 2 {
		t.Errorf("기록 폴더에 다른 것이 생겼다: %v", entries)
	}
}

func TestDictationLogRecordsFailures(t *testing.T) {
	logPath := filepath.Join(t.TempDir(), "dictation-log.jsonl")
	do(logServer(t, &fakeTranscriber{err: errors.New("받아쓰기 명령 실패: CUDA failed")}, "", logPath), audioPost("rec", "audio/webm"))
	got := readDictationLog(t, logPath)
	if len(got) != 1 || got[0].Result != "error" || !strings.Contains(got[0].Error, "CUDA failed") {
		t.Errorf("오류 줄: %+v", got)
	}
}

// 폰이 끊은 받아쓰기도 남긴다 — «말했는데 글이 안 왔다» 를 나중에 가릴 수 있게.
func TestDictationLogRecordsAPhoneThatLeft(t *testing.T) {
	logPath := filepath.Join(t.TempDir(), "dictation-log.jsonl")
	tr := &fakeTranscriber{block: make(chan struct{}), started: make(chan struct{})}
	h := logServer(t, tr, "", logPath)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		do(h, audioPost("rec", "audio/webm").WithContext(ctx))
		close(done)
	}()
	<-tr.started
	cancel()
	<-done
	got := readDictationLog(t, logPath)
	if len(got) != 1 || got[0].Result != "error" || !strings.Contains(got[0].Error, "canceled") {
		t.Errorf("끊긴 줄: %+v", got)
	}
}

// 경로가 비면(제로값) 아무 데도 쓰지 않는다 — go test 가 개발자의 ~/.config/mux 에 닿지 않게.
func TestNoDictationLogWithoutAPath(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, ".config"))
	do(whisperServer(t, newFake(), &fakeTranscriber{text: "안녕"}, "", nil), audioPost("rec", "audio/webm"))
	if entries, _ := os.ReadDir(home); len(entries) != 0 {
		t.Errorf("경로 없이 무언가를 썼다: %v", entries)
	}
}

func TestDictationLogRollsOverOnce(t *testing.T) {
	logPath := filepath.Join(t.TempDir(), "dictation-log.jsonl")
	old := strings.Repeat("x", maxDictationLog)
	os.WriteFile(logPath, []byte(old), 0o600)
	do(logServer(t, &fakeTranscriber{text: "안녕"}, "", logPath), audioPost("rec", "audio/webm"))
	if data, _ := os.ReadFile(logPath + ".1"); len(data) != len(old) {
		t.Errorf(".1 로 넘긴 기록 %d바이트, want %d", len(data), len(old))
	}
	if got := readDictationLog(t, logPath); len(got) != 1 || got[0].Text != "안녕" {
		t.Errorf("넘긴 뒤 새 기록: %+v", got)
	}
}

func TestCommandTranscriberReadsDurations(t *testing.T) {
	got, err := fakeCommand(t, "info").Transcribe(context.Background(), audioFile(t, "녹음"), "")
	if err != nil || got.Text != "안녕" || got.Audio != 12.5 || got.Speech != 9.25 {
		t.Errorf("Transcribe = %+v, %v", got, err)
	}
}
