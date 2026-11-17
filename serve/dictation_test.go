package serve

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func TestParseDictation(t *testing.T) {
	d := parseDictation(strings.Join([]string{
		"# 용어는 프롬프트로, 틀린 말 => 바른 말 은 받아쓴 뒤 바꿔 쓰기로",
		"herdr",
		"  golangci-lint  ",
		"",
		"고랑 쉐이 린트 => golangci-lint",
		"고 베트=>go vet",
		"음 =>", // 지우기
		" => 빈 왼쪽은 버린다",
		"herdr", // 같은 용어는 한 번
	}, "\n"))
	if want := []string{"herdr", "golangci-lint"}; !reflect.DeepEqual(d.terms, want) {
		t.Errorf("terms = %q, want %q", d.terms, want)
	}
	want := []dictationFix{{"고랑 쉐이 린트", "golangci-lint"}, {"고 베트", "go vet"}, {"음", ""}}
	if !reflect.DeepEqual(d.fixes, want) {
		t.Errorf("fixes = %q, want %q", d.fixes, want)
	}
}

func TestDictationFixIsLiteralAndInOrder(t *testing.T) {
	d := dictation{fixes: []dictationFix{{"고 베트", "go vet"}, {"go vet", "go vet ./..."}, {"a.c", "X"}}}
	got := d.fix("고 베트하고 abc 와 a.c")
	// 앞의 바꿔 쓰기 결과에 뒤의 것이 이어 걸린다(파일 순서), 점은 정규식이 아니라 글자다.
	if want := "go vet ./...하고 abc 와 X"; got != want {
		t.Errorf("fix = %q, want %q", got, want)
	}
}

func TestReadDictationWithoutAFile(t *testing.T) {
	for _, path := range []string{"", filepath.Join(t.TempDir(), "없음.txt")} {
		d, err := readDictation(path)
		if err != nil || len(d.terms) != 0 || len(d.fixes) != 0 {
			t.Errorf("readDictation(%q) = %+v, %v — 파일이 없으면 빈 것", path, d, err)
		}
	}
	path := filepath.Join(t.TempDir(), "dictation.txt")
	if err := os.WriteFile(path, []byte("mux\n묵스 => mux\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	d, err := readDictation(path)
	if err != nil || !reflect.DeepEqual(d.terms, []string{"mux"}) || len(d.fixes) != 1 {
		t.Errorf("readDictation = %+v, %v", d, err)
	}
}

func TestDictationPrompt(t *testing.T) {
	cases := []struct {
		name            string
		sessions, terms []string
		want            string
	}{
		{"비면 프롬프트도 없다", nil, nil, ""},
		// 숫자뿐인 이름(tmux 의 기본 이름)은 용어가 아니다. 파일 용어와 겹치는 세션 이름은 뒤(파일 쪽)에 한 번만 —
		// faster-whisper 는 프롬프트의 끝을 남기고 앞을 자른다.
		{"세션 이름이 앞, 파일 용어가 뒤", []string{"nse", "0", "MUX", "nse"}, []string{"herdr", "mux"},
			"nse, herdr, mux."},
		{"파일만", nil, []string{"systemd"}, "systemd."},
	}
	for _, c := range cases {
		if got := dictationPrompt(c.sessions, c.terms); got != c.want {
			t.Errorf("%s: %q, want %q", c.name, got, c.want)
		}
	}
}

func TestDictationPromptDropsFromTheFront(t *testing.T) {
	var sessions []string
	for i := range 400 {
		sessions = append(sessions, "session-"+strings.Repeat("x", i%7)+string(rune('a'+i%26)))
	}
	got := dictationPrompt(sessions, []string{"herdr", "golangci-lint"})
	if len(got) > maxDictationPrompt {
		t.Fatalf("길이 %d 가 상한 %d 를 넘었다", len(got), maxDictationPrompt)
	}
	if !strings.HasPrefix(got, "session-") || !strings.HasSuffix(got, "herdr, golangci-lint.") {
		t.Errorf("넘치면 앞의 세션 이름부터 버리고 파일 용어를 지킨다: %q…%q", got[:40], got[len(got)-40:])
	}
}
