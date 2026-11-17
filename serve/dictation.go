package serve

import (
	"errors"
	"os"
	"strings"
)

// 받아쓰기 용어 파일(기본 ~/.config/mux/dictation.txt, docs/design/serve.md «받아쓰기 — 서버 Whisper»).
// 한 줄에 하나: `용어` 는 Whisper 의 프롬프트로, `틀린 말 => 바른 말` 은 받아쓴 뒤 바꿔 쓰기로, `#` 은 주석.
// 요청마다 읽는다 — 고친 뒤 mux serve 를 다시 띄울 필요가 없다.

// maxDictationPrompt 는 프롬프트의 바이트 상한이다. faster-whisper 는 프롬프트의 끝 223 토큰만 쓰고(용어 26개가
// 97 토큰이었다) 정확한 자르기는 그쪽이 한다. 여기서는 argv 가 끝없이 커지지 않게만 막는다.
const maxDictationPrompt = 2048

type dictationFix struct{ from, to string }

type dictation struct {
	terms []string
	fixes []dictationFix
}

func parseDictation(text string) dictation {
	var d dictation
	seen := map[string]bool{}
	for _, line := range strings.Split(text, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		if from, to, ok := strings.Cut(line, "=>"); ok {
			if from = strings.TrimSpace(from); from != "" {
				d.fixes = append(d.fixes, dictationFix{from: from, to: strings.TrimSpace(to)})
			}
			continue
		}
		if !seen[line] {
			seen[line] = true
			d.terms = append(d.terms, line)
		}
	}
	return d
}

// readDictation 은 파일을 읽는다. 경로가 비었거나 파일이 없으면 빈 것이다(세션 이름만으로 프롬프트를 만든다).
func readDictation(path string) (dictation, error) {
	if path == "" {
		return dictation{}, nil
	}
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return dictation{}, nil
	}
	if err != nil {
		return dictation{}, err
	}
	return parseDictation(string(data)), nil
}

// fix 는 바꿔 쓰기를 파일 순서대로 건다. 글자 그대로다(정규식이 아니다) — 잘못 바뀌어도 사람이 입력칸에서 보고
// 고친다(저절로 보내지 않는다).
func (d dictation) fix(text string) string {
	for _, f := range d.fixes {
		text = strings.ReplaceAll(text, f.from, f.to)
	}
	return text
}

// dictationPrompt 는 세션 이름, 그 뒤에 파일의 용어를 쉼표로 늘어놓은 목록이다(2026-11-17 측정, serve.md):
//   - 문장으로 쓴 프롬프트는 «kitty 기준» 을 «pt기즈», mux 를 «mx» 로 바꿨다.
//   - «개발 대화. 용어: » 같은 머리말을 붙이면 3초짜리 «mux 패널에서» 가 «용어. mux 패널에서» 로 나왔다 — 짧은 말에서
//     프롬프트의 글이 받아쓴 글로 샌다. 목록만 두면 새지 않았다.
//
// faster-whisper 는 넘치는 프롬프트의 앞을 자르므로, 사람이 고른 파일 용어를 뒤에 두어 세션 이름부터 잘리게 한다.
// 숫자뿐인 세션 이름(tmux 의 기본 이름)은 용어가 아니다.
func dictationPrompt(sessions, terms []string) string {
	inTerms := map[string]bool{}
	for _, t := range terms {
		inTerms[strings.ToLower(t)] = true
	}
	var items []string
	seen := map[string]bool{}
	for _, s := range sessions {
		key := strings.ToLower(s)
		if s == "" || allDigits(s) || inTerms[key] || seen[key] {
			continue
		}
		seen[key] = true
		items = append(items, s)
	}
	items = append(items, terms...)
	for len(items) > 0 && len(strings.Join(items, ", "))+1 > maxDictationPrompt {
		items = items[1:]
	}
	if len(items) == 0 {
		return ""
	}
	return strings.Join(items, ", ") + "."
}

func allDigits(s string) bool {
	for _, r := range s {
		if r < '0' || r > '9' {
			return false
		}
	}
	return true
}
