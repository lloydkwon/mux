//go:build e2e

// fakeopencode 는 e2e 가 보관(p)·보관함(P)을 시험하려고 격리 tmux 의 pane 에 띄우는 가짜 opencode 다.
// 실행 파일 이름이 opencode 여야 mux 가 /proc 에서 알아본다 — e2e 는 «…/opencode» 로 빌드한다.
//
// 진짜 opencode 2.0.29·2.2.7 에서 잰 것만 흉내 낸다(docs/design/park.md):
//   - 대화형 화면(TUI): 하위 명령 없이, 또는 -s <id> 로 뜬다. 한 줄을 받으면 대화가 없을 때 새로 만들고,
//     있으면 그 대화의 updated 를 새로 쓴다. 화면에는 «session: <id>» 를 그린다.
//   - `api session.list --param directory=<d> …`: 그 폴더의 대화를 {"data":[…]} 로, 갱신순.
//   - `api session.get --param sessionID=<id>`: 있으면 {"data":{…}}, 없으면 종료 코드 1 에 stdout 의
//     SessionNotFoundError 본문과 stderr 의 «HTTP 404 Not Found» 줄(2.2.7).
//
// 대화는 $HOME/.fakeopencode/<id>.json 하나씩이다.
package main

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"
)

type session struct {
	ID    string `json:"id"`
	Title string `json:"title,omitempty"`
	Time  struct {
		Created int64 `json:"created"`
		Updated int64 `json:"updated"`
	} `json:"time"`
	Location struct {
		Directory string `json:"directory"`
	} `json:"location"`
}

func storeDir() string { return filepath.Join(os.Getenv("HOME"), ".fakeopencode") }

func load(id string) (session, bool) {
	var s session
	data, err := os.ReadFile(filepath.Join(storeDir(), id+".json"))
	if err != nil || json.Unmarshal(data, &s) != nil {
		return session{}, false
	}
	return s, true
}

func save(s session) {
	_ = os.MkdirAll(storeDir(), 0o755)
	data, _ := json.Marshal(s)
	_ = os.WriteFile(filepath.Join(storeDir(), s.ID+".json"), data, 0o644)
}

// param 은 «--param key=value» 들에서 key 의 값을 찾는다.
func param(args []string, key string) string {
	for i := 0; i+1 < len(args); i++ {
		if args[i] == "--param" {
			if k, v, ok := strings.Cut(args[i+1], "="); ok && k == key {
				return v
			}
		}
	}
	return ""
}

func api(args []string) {
	switch args[0] {
	case "session.list":
		dir := param(args, "directory")
		entries, _ := os.ReadDir(storeDir())
		list := []session{}
		for _, e := range entries {
			if s, ok := load(strings.TrimSuffix(e.Name(), ".json")); ok && s.Location.Directory == dir {
				list = append(list, s)
			}
		}
		sort.Slice(list, func(i, j int) bool { return list[i].Time.Updated > list[j].Time.Updated })
		out, _ := json.Marshal(map[string]any{"data": list, "cursor": map[string]any{}})
		fmt.Println(string(out))
	case "session.get":
		id := param(args, "sessionID")
		if s, ok := load(id); ok {
			out, _ := json.Marshal(map[string]any{"data": s})
			fmt.Println(string(out))
			return
		}
		fmt.Printf(`{"_tag":"SessionNotFoundError","sessionID":%q,"message":"Session not found: %s"}`+"\n", id, id)
		fmt.Fprintln(os.Stderr, "HTTP 404 Not Found")
		os.Exit(1)
	default:
		fmt.Fprintln(os.Stderr, "fakeopencode: unknown api", args[0])
		os.Exit(1)
	}
}

func tui(args []string) {
	current := ""
	for i := 0; i < len(args); i++ {
		if args[i] == "-s" && i+1 < len(args) {
			current = args[i+1]
			i++
		}
	}
	cwd, _ := os.Getwd()
	draw := func(last string) {
		fmt.Print("\033[H\033[2J")
		fmt.Println("fake opencode")
		if current == "" {
			fmt.Println("session: none")
		} else {
			fmt.Println("session: " + current)
		}
		if last != "" {
			fmt.Println("echo: " + last)
		}
	}
	draw("")
	in := bufio.NewScanner(os.Stdin)
	for in.Scan() {
		line := strings.TrimSpace(in.Text())
		if line == "" {
			continue
		}
		now := time.Now().UnixMilli()
		s, ok := load(current)
		if !ok {
			s = session{ID: "ses_" + strconv.FormatInt(time.Now().UnixNano(), 36), Title: line}
			s.Time.Created = now
			s.Location.Directory = cwd
			current = s.ID
		}
		s.Time.Updated = now
		save(s)
		draw(line)
	}
}

func main() {
	args := os.Args[1:]
	if len(args) >= 2 && args[0] == "api" {
		api(args[1:])
		return
	}
	tui(args)
}
