package tmux

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"
)

// 이벤트 로그는 tmux 전역 옵션에 살고, 그건 서버와 함께 죽는다. 그게 맞다고 오래
// 적어 두었는데, 전제가 하나 숨어 있었다: *서버의 수명 ≈ 사용자의 수명*.
//
// 2026-09-01 그 전제가 깨졌다. /tmp 이 비워지며 소켓이 unlink 됐고, 서버는 세션도
// 프로세스도 전부 살아 있는 채로 아무에게도 닿지 않게 됐다. 새 서버가 그 자리를
// 이어받았고 로그는 통째로 사라졌다 — 잃을 이유가 없는 것을 잃었다.
//
// 그래서 옵션은 그대로 두고 파일을 하나 옆에 둔다. 역할이 다르다:
//
//	옵션  @mux_events    서버 수명, liveness 로 자름, 패널이 매 틱 읽어 그리는 것
//	파일  events.json    머신 수명, liveness 로 자르지 않음, 되살릴 때만 읽는 보관본
//
// "아무도 청소하지 않아도 된다"는 원래 논거는 유지된다 — 14일 컷오프가 그 일을 한다.

const (
	// MaxArchiveEvents 는 보관본의 건수 상한.
	//
	// 바이트가 아니라 건수인 이유: 옵션을 묶던 16KB MAX_IMSGSIZE 벽은 set-option
	// argv 제한이라 파일에는 없다. 바이트로 자르면 잘리는 지점이 Text 길이(=클로드가
	// 지은 작업명)에 따라 달라져, 패널마다 다른 데서 잘라 서로를 향해 영원히 다시 쓴다.
	//
	// 2000건이면 ~90바이트/건으로 ~180KB. CLAUDE.md 가 기록한 속도(64분에 50건)로
	// 쉬지 않고 바빠도 40시간치라, 실질 경계는 아래 나이 컷오프다.
	MaxArchiveEvents = 2000

	// archiveMaxAgeMillis 는 이보다 오래된 항목을 버린다.
	//
	// 건수 상한만으로는 안 된다. 3번 규칙(세션당 하나 남기기)이 나이 없이 돌면
	// *존재했던 모든 세션 이름*의 항목을 영원히 붙들고, 그건 VS Code 프로필이
	// 만들어내는 이름 churn 에 그대로 물리는 무한 증가다.
	archiveMaxAgeMillis = 14 * 24 * 60 * 60 * 1000

	// archiveVersion 은 파일 스키마 버전.
	//
	// 옵션에는 없고 파일에만 있는 이유: 이 파일은 자기를 쓴 바이너리보다 오래 산다.
	// 그게 이 파일의 존재 이유이기도 하다.
	archiveVersion = 1
)

// eventArchiveFile 은 보관본의 위치. panelStateFile 과 같은 이음매다 —
// 없으면 병합을 도는 모든 테스트가 개발자의 실제 ~/.config/mux 에 쓴다.
var eventArchiveFile = defaultEventArchivePath

// nowMillis 는 나이 컷오프의 기준. 테스트가 시계를 고정할 수 있게 var.
var nowMillis = func() int64 { return time.Now().UnixMilli() }

type eventArchive struct {
	Version int          `json:"v"`
	Events  []PanelEvent `json:"events"`
}

func defaultEventArchivePath() (string, error) {
	dir, err := os.UserConfigDir()
	if err != nil {
		return "", fmt.Errorf("find config directory: %w", err)
	}
	return filepath.Join(dir, "mux", "events.json"), nil
}

// loadArchive 는 보관본을 최신순으로 읽는다. 모든 실패는 빈 목록이다 —
// loadEvents 와 savedPanelWidthFrom 이 취하는 자세 그대로. 없는 파일은 첫 실행이고,
// 손상된 파일 때문에 패널이 그리기를 거부하는 것은 더 나쁘다.
func loadArchive() []PanelEvent {
	path, err := eventArchiveFile()
	if err != nil {
		return nil
	}
	return loadArchiveFrom(path)
}

func loadArchiveFrom(path string) []PanelEvent {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil
	}
	var a eventArchive
	if err := json.Unmarshal(data, &a); err != nil {
		return nil
	}
	sortEvents(a.Events)
	return a.Events
}

func saveArchive(events []PanelEvent) {
	path, err := eventArchiveFile()
	if err != nil {
		return
	}
	_ = saveArchiveTo(path, events)
}

// saveArchiveTo 는 panelwidth.go 의 원자적 쓰기를 그대로 따른다. 다른 점은 하나,
// 들여쓰기를 하지 않는다 — 이건 사람이 읽는 설정이 아니라 180KB 기계 데이터다.
func saveArchiveTo(path string, events []PanelEvent) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return fmt.Errorf("create event archive directory: %w", err)
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), ".events-*.tmp")
	if err != nil {
		return fmt.Errorf("create temporary event archive: %w", err)
	}
	tmpName := tmp.Name()
	ok := false
	defer func() {
		_ = tmp.Close()
		if !ok {
			_ = os.Remove(tmpName)
		}
	}()

	if err := tmp.Chmod(0o600); err != nil {
		return fmt.Errorf("set event archive permissions: %w", err)
	}
	if err := json.NewEncoder(tmp).Encode(eventArchive{Version: archiveVersion, Events: events}); err != nil {
		return fmt.Errorf("write event archive: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("close event archive: %w", err)
	}
	if err := os.Rename(tmpName, path); err != nil {
		return fmt.Errorf("replace event archive: %w", err)
	}
	ok = true
	return nil
}

// missingSessions 는 살아 있는데 로그에 항목이 하나도 없는 세션들이다.
//
// backfill 을 이걸로 키잉하는 것이 설계의 핵심이다. "옵션이 비면 파일에서 읽는다"는
// 딱 한 번, 최악의 순간에 발동한다 — 서버가 죽은 직후 옵션은 비었지만 세션은 아직
// 한둘만 돌아와 있어서, 그 시드가 즉시 그 한둘로 잘리고 3분 뒤 돌아오는 세션은
// 영영 이력을 못 받는다. 세션별로 물으면 세션마다 돌아온 그 순간에 채워진다.
//
// live 가 nil 이면("전부 유지") 아무것도 채우지 않는다. 무엇이 살아 있는지 모르면
// 죽은 세션을 되살릴 위험이 있고, 되살리지 않는 쪽이 언제나 덜 나쁘다.
func missingSessions(events []PanelEvent, live []string) map[string]bool {
	if len(live) == 0 {
		return nil
	}
	seen := make(map[string]bool, len(events))
	for _, e := range events {
		seen[e.Session] = true
	}
	missing := make(map[string]bool)
	for _, name := range live {
		if !seen[name] {
			missing[name] = true
		}
	}
	if len(missing) == 0 {
		return nil
	}
	return missing
}

// unionEvents 는 중복 없이 합친다. 동일성 판단은 옵션 병합과 같은 duplicateAt 이라,
// 파일과 옵션이 같은 전이를 같은 항목으로 본다.
func unionEvents(base []PanelEvent, extra ...[]PanelEvent) []PanelEvent {
	out := append(base[:0:0], base...)
	for _, group := range extra {
		for _, e := range group {
			if duplicateAt(out, e) >= 0 {
				continue
			}
			out = append(out, e)
		}
	}
	sortEvents(out)
	return out
}

// trimArchive 는 보관본을 자른다. trimLog 과 달리 liveness 를 보지 않는다 —
// 죽은 세션의 이력을 버리는 것이 바로 고치려는 버그다.
//
// trimLog 을 재사용하지 않는 이유는 그 함수의 "세션당 하나 남기기"가 *그리기* 규칙,
// 즉 "패널이 살아 있는 세션마다 한 줄을 그린다"에서 나온 것이기 때문이다. liveness
// 입력이 없는 저장소에 그대로 쓰면 경계가 사라진다. 여기서는 그 통찰을 liveness 가
// 아니라 *나이*로 묶는다.
//
// events 는 최신순이어야 한다.
func trimArchive(events []PanelEvent, now int64) []PanelEvent {
	cutoff := now - archiveMaxAgeMillis
	fresh := events[:0:0]
	for _, e := range events {
		if e.At >= cutoff {
			fresh = append(fresh, e)
		}
	}
	if len(fresh) <= MaxArchiveEvents {
		return fresh
	}

	kept := fresh[:MaxArchiveEvents:MaxArchiveEvents]
	seen := make(map[string]bool, len(kept))
	for _, e := range kept {
		seen[e.Session] = true
	}
	for _, e := range fresh[MaxArchiveEvents:] {
		if seen[e.Session] {
			continue
		}
		seen[e.Session] = true
		kept = append(kept, e)
	}
	return kept
}

// sameEvents 는 직렬화 결과로 비교한다.
//
// 옵션처럼 원본 바이트와 대조하지 않는 이유: 파일 바이트는 공백·개행·손편집으로
// 내용이 같아도 달라질 수 있고, 그러면 패널 일곱이 매번 같은 내용을 서로에게 다시 쓴다.
func sameEvents(a, b []PanelEvent) bool {
	ea, err1 := json.Marshal(a)
	eb, err2 := json.Marshal(b)
	if err1 != nil || err2 != nil {
		return false
	}
	return string(ea) == string(eb)
}
