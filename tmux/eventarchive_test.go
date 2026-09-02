package tmux

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

// freezeNow 는 나이 컷오프의 기준을 고정한다. 픽스처가 쓰는 작은 타임스탬프는
// 실제 시각 기준으로는 14일보다 훨씬 오래된 것이라 전부 잘려 나간다.
func freezeNow(t *testing.T, now int64) {
	t.Helper()
	old := nowMillis
	nowMillis = func() int64 { return now }
	t.Cleanup(func() { nowMillis = old })
}

func archivePath(t *testing.T) string {
	t.Helper()
	p, err := eventArchiveFile()
	if err != nil {
		t.Fatalf("보관본 경로: %v", err)
	}
	return p
}

func seedArchive(t *testing.T, events []PanelEvent) {
	t.Helper()
	if err := saveArchiveTo(archivePath(t), events); err != nil {
		t.Fatalf("보관본 시드: %v", err)
	}
}

// 서버가 죽고 세션이 돌아왔을 때 이력이 따라 돌아온다 — 이 기능의 요구사항 자체.
func TestMergeEventsBackfillsASessionThatCameBack(t *testing.T) {
	freezeNow(t, 1000)
	withMock(t, func(m *mockRunner) {
		seedArchive(t, []PanelEvent{ev("b", AIStateReady, 200, 200), ev("a", AIStateReady, 100, 100)})
		mockEventLog(m, []PanelEvent{ev("a", AIStateWorking, 300, 300)})

		got := MergeEvents(nil, []string{"a", "b"})

		var haveB bool
		for _, e := range got {
			if e.Session == "b" {
				haveB = true
			}
		}
		if !haveB {
			t.Fatalf("돌아온 세션 b 의 이력이 채워지지 않았다: %+v", got)
		}
		if !contains(writtenLog(m), `"s":"b"`) {
			t.Fatalf("옵션에 b 가 쓰이지 않았다: %s", writtenLog(m))
		}
	})
}

// 이 테스트 하나가 설계 전체를 담고 있다. 지우면 기능이 조용히 버그로 되돌아간다:
// trimLog 이 옵션에서 버린 죽은 세션의 이력이 파일에는 남아 있어야, 그 세션이
// 돌아왔을 때 채워 넣을 것이 있다.
func TestArchiveKeepsWhatTrimLogDropped(t *testing.T) {
	freezeNow(t, 1000)
	withMock(t, func(m *mockRunner) {
		mockEventLog(m, []PanelEvent{
			ev("alive", AIStateWorking, 200, 200),
			ev("gone", AIStateReady, 100, 100),
		})

		MergeEvents([]PanelEvent{ev("alive", AIStateReady, 300, 300)}, []string{"alive"})

		if contains(writtenLog(m), `"s":"gone"`) {
			t.Fatalf("옵션이 죽은 세션을 그대로 뒀다: %s", writtenLog(m))
		}
		kept := loadArchiveFrom(archivePath(t))
		var haveGone bool
		for _, e := range kept {
			if e.Session == "gone" {
				haveGone = true
			}
		}
		if !haveGone {
			t.Fatalf("보관본이 죽은 세션의 이력을 버렸다: %+v", kept)
		}
	})
}

// 보관본에만 있는 세션은 옵션으로 새어 나가지 않는다 — 패널이 없는 세션을 그리면 안 된다.
func TestMergeEventsDoesNotResurrectDeadSessions(t *testing.T) {
	freezeNow(t, 1000)
	withMock(t, func(m *mockRunner) {
		seedArchive(t, []PanelEvent{ev("gone", AIStateReady, 100, 100)})
		mockEventLog(m, []PanelEvent{ev("a", AIStateWorking, 300, 300)})

		got := MergeEvents(nil, []string{"a"})

		for _, e := range got {
			if e.Session == "gone" {
				t.Fatalf("죽은 세션을 되살렸다: %+v", got)
			}
		}
	})
}

// 한가한 틱에는 파일을 열지도 않는다. 패널 일곱이 2초마다 180KB 를 파싱하면 안 된다.
func TestIdleMergeTouchesNoFile(t *testing.T) {
	freezeNow(t, 1000)
	withMock(t, func(m *mockRunner) {
		mockEventLog(m, []PanelEvent{ev("a", AIStateWorking, 300, 300)})
		path := archivePath(t)

		MergeEvents(nil, []string{"a"})

		if _, err := os.Stat(path); !os.IsNotExist(err) {
			t.Fatalf("한가한 병합이 보관본을 건드렸다 (err=%v)", err)
		}
	})
}

// 내용이 같으면 다시 쓰지 않는다.
func TestArchiveWriteSkippedWhenUnchanged(t *testing.T) {
	freezeNow(t, 1000)
	withMock(t, func(m *mockRunner) {
		mockEventLog(m, nil)
		MergeEvents([]PanelEvent{ev("a", AIStateWorking, 300, 300)}, []string{"a"})

		path := archivePath(t)
		fi, err := os.Stat(path)
		if err != nil {
			t.Fatalf("첫 병합이 보관본을 쓰지 않았다: %v", err)
		}
		before := fi.ModTime()

		// 옵션이 이미 그 항목을 들고 있는 상태로 같은 병합을 반복.
		mockEventLog(m, []PanelEvent{ev("a", AIStateWorking, 300, 300)})
		MergeEvents([]PanelEvent{ev("a", AIStateWorking, 300, 300)}, []string{"a"})

		fi2, _ := os.Stat(path)
		if !fi2.ModTime().Equal(before) {
			t.Fatal("바뀐 것이 없는데 보관본을 다시 썼다")
		}
	})
}

// 진 경합은 다음 틱에 옵션에서 복원된다.
func TestArchiveUnionRepairsALostRace(t *testing.T) {
	freezeNow(t, 1000)
	withMock(t, func(m *mockRunner) {
		// 파일에는 X 가 없고 옵션에는 있다 — 다른 패널이 rename 경합에서 이겼을 때의 모습.
		seedArchive(t, []PanelEvent{ev("a", AIStateReady, 100, 100)})
		mockEventLog(m, []PanelEvent{
			ev("a", AIStateWorking, 300, 300), // X
			ev("a", AIStateReady, 100, 100),
		})

		MergeEvents([]PanelEvent{ev("b", AIStateReady, 400, 400)}, []string{"a", "b"})

		kept := loadArchiveFrom(archivePath(t))
		var haveX bool
		for _, e := range kept {
			if e.Session == "a" && e.State == AIStateWorking {
				haveX = true
			}
		}
		if !haveX {
			t.Fatalf("옵션에 있던 항목이 보관본으로 회복되지 않았다: %+v", kept)
		}
	})
}

func TestArchiveRoundTripIsAtomicAndOwnerOnly(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "events.json")
	want := []PanelEvent{ev("a", AIStateReady, 200, 200), ev("b", AIStateWorking, 100, 100)}

	if err := saveArchiveTo(path, want); err != nil {
		t.Fatalf("저장: %v", err)
	}
	if got := loadArchiveFrom(path); len(got) != 2 || got[0].Session != "a" {
		t.Fatalf("왕복 결과 %+v", got)
	}
	if err := saveArchiveTo(path, want[:1]); err != nil {
		t.Fatalf("재저장: %v", err)
	}
	if got := loadArchiveFrom(path); len(got) != 1 {
		t.Fatalf("덮어쓰기가 아니라 덧붙였다: %+v", got)
	}

	entries, _ := os.ReadDir(dir)
	if len(entries) != 1 {
		t.Fatalf("임시 파일이 남았다: %d개", len(entries))
	}
	fi, _ := os.Stat(path)
	if fi.Mode().Perm() != 0o600 {
		t.Fatalf("권한 %v", fi.Mode().Perm())
	}
}

// 읽기 실패는 전부 빈 목록이다 — 손상된 파일 때문에 패널이 멈추면 안 된다.
func TestArchiveDegradesToEmpty(t *testing.T) {
	dir := t.TempDir()
	cases := map[string]func(string){
		"없는 파일":   func(string) {},
		"깨진 JSON": func(p string) { _ = os.WriteFile(p, []byte("{not json"), 0o600) },
		"타입 불일치":  func(p string) { _ = os.WriteFile(p, []byte(`{"v":1,"events":"nope"}`), 0o600) },
		"디렉터리":    func(p string) { _ = os.Mkdir(p, 0o700) },
	}
	for name, setup := range cases {
		t.Run(name, func(t *testing.T) {
			p := filepath.Join(dir, name)
			setup(p)
			if got := loadArchiveFrom(p); len(got) != 0 {
				t.Fatalf("%+v", got)
			}
		})
	}
}

func TestTrimArchiveCapsByCountAndAge(t *testing.T) {
	const now = int64(1_000_000_000_000)
	old := now - archiveMaxAgeMillis - 1

	t.Run("나이로 자른다", func(t *testing.T) {
		got := trimArchive([]PanelEvent{
			ev("a", AIStateReady, now, now),
			ev("a", AIStateReady, old, old),
		}, now)
		if len(got) != 1 || got[0].At != now {
			t.Fatalf("%+v", got)
		}
	})

	t.Run("건수로 자르되 조용한 세션 하나는 남긴다", func(t *testing.T) {
		var in []PanelEvent
		for i := 0; i < MaxArchiveEvents+10; i++ {
			in = append(in, ev("loud", AIStateWorking, now-int64(i), 0))
		}
		in = append(in, ev("quiet", AIStateReady, now-int64(MaxArchiveEvents+100), 0))

		got := trimArchive(in, now)

		if len(got) != MaxArchiveEvents+1 {
			t.Fatalf("%d건", len(got))
		}
		if got[len(got)-1].Session != "quiet" {
			t.Fatalf("조용한 세션이 밀려났다: %+v", got[len(got)-1])
		}
	})

	t.Run("나이 컷오프가 세션당 하나보다 먼저다", func(t *testing.T) {
		var in []PanelEvent
		for i := 0; i < MaxArchiveEvents+10; i++ {
			in = append(in, ev("loud", AIStateWorking, now-int64(i), 0))
		}
		in = append(in, ev("ancient", AIStateReady, old, 0))

		for _, e := range trimArchive(in, now) {
			if e.Session == "ancient" {
				t.Fatal("14일 지난 항목이 세션당 하나 규칙으로 살아남았다")
			}
		}
	})
}

// 패널 일곱이 같은 집합에서 같은 바이트를 낸다. 아니면 서로를 향해 영원히 다시 쓴다.
func TestArchiveSerializationIsDeterministic(t *testing.T) {
	a := []PanelEvent{ev("a", AIStateReady, 100, 100), ev("b", AIStateWorking, 200, 200)}
	b := []PanelEvent{ev("b", AIStateWorking, 200, 200), ev("a", AIStateReady, 100, 100)}
	sortEvents(a)
	sortEvents(b)
	ea, _ := json.Marshal(a)
	eb, _ := json.Marshal(b)
	if string(ea) != string(eb) {
		t.Fatalf("직렬화가 갈린다:\n%s\n%s", ea, eb)
	}
}

func contains(haystack, needle string) bool {
	return len(haystack) >= len(needle) && stringsIndex(haystack, needle) >= 0
}

func stringsIndex(h, n string) int {
	for i := 0; i+len(n) <= len(h); i++ {
		if h[i:i+len(n)] == n {
			return i
		}
	}
	return -1
}
