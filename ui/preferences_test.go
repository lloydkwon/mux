package ui

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"
	"time"

	"github.com/lloydkwon/mux/tmux"
)

func TestSortModeRotation(t *testing.T) {
	prefs := defaultPreferences()
	for _, want := range []sortMode{sortName, sortOrder, sortRecent} {
		prefs = prefs.nextSort()
		if prefs.Sort != want {
			t.Fatalf("next sort = %q, want %q", prefs.Sort, want)
		}
	}
}

func TestSortedSessions(t *testing.T) {
	now := time.Now()
	sessions := []tmux.Session{
		{Name: "zeta", Activity: now.Add(-time.Minute)},
		{Name: "Alpha", Activity: now.Add(-time.Hour)},
		{Name: "beta", Activity: now},
	}

	tests := []struct {
		name  string
		prefs preferences
		want  []string
	}{
		{
			name:  "recent activity",
			prefs: preferences{Sort: sortRecent},
			want:  []string{"beta", "zeta", "Alpha"},
		},
		{
			name:  "alphabetical",
			prefs: preferences{Sort: sortName},
			want:  []string{"Alpha", "beta", "zeta"},
		},
		{
			name: "explicit order then unordered by name",
			prefs: preferences{
				Sort:   sortOrder,
				Orders: map[string]int{"zeta": 20, "beta": 2},
			},
			want: []string{"beta", "zeta", "Alpha"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			gotSessions := sortedSessions(sessions, tt.prefs)
			got := make([]string, len(gotSessions))
			for i := range gotSessions {
				got[i] = gotSessions[i].Name
			}
			if !reflect.DeepEqual(got, tt.want) {
				t.Fatalf("sorted names = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestPreferencesRoundTrip(t *testing.T) {
	path := filepath.Join(t.TempDir(), "mux", "preferences.json")
	want := preferences{
		Sort:   sortOrder,
		Orders: map[string]int{"news": 1, "worker": 12},
	}
	if err := savePreferencesTo(path, want); err != nil {
		t.Fatal(err)
	}
	got, err := loadPreferencesFrom(path)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("loaded preferences = %#v, want %#v", got, want)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("preferences mode = %o, want 600", info.Mode().Perm())
	}
}

func TestPreferencesNormalizeInvalidValues(t *testing.T) {
	path := filepath.Join(t.TempDir(), "preferences.json")
	if err := os.WriteFile(path, []byte(`{"sort":"unknown","orders":{"keep":3,"zero":0,"negative":-1}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	got, err := loadPreferencesFrom(path)
	if err != nil {
		t.Fatal(err)
	}
	if got.Sort != sortRecent {
		t.Fatalf("sort = %q, want %q", got.Sort, sortRecent)
	}
	if !reflect.DeepEqual(got.Orders, map[string]int{"keep": 3}) {
		t.Fatalf("orders = %#v", got.Orders)
	}
}
