package ui

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/xguru/mux/tmux"
)

type sortMode string

const (
	sortRecent sortMode = "recent"
	sortName   sortMode = "name"
	sortOrder  sortMode = "order"
)

type preferences struct {
	Sort   sortMode       `json:"sort"`
	Orders map[string]int `json:"orders,omitempty"`
}

func defaultPreferences() preferences {
	return preferences{
		Sort:   sortRecent,
		Orders: make(map[string]int),
	}
}

func (p preferences) normalized() preferences {
	switch p.Sort {
	case sortRecent, sortName, sortOrder:
	default:
		p.Sort = sortRecent
	}
	if p.Orders == nil {
		p.Orders = make(map[string]int)
	}
	for name, order := range p.Orders {
		if strings.TrimSpace(name) == "" || order <= 0 {
			delete(p.Orders, name)
		}
	}
	return p
}

func (p preferences) nextSort() preferences {
	switch p.Sort {
	case sortRecent:
		p.Sort = sortName
	case sortName:
		p.Sort = sortOrder
	default:
		p.Sort = sortRecent
	}
	return p
}

func preferencesPath() (string, error) {
	dir, err := os.UserConfigDir()
	if err != nil {
		return "", fmt.Errorf("find config directory: %w", err)
	}
	return filepath.Join(dir, "mux", "preferences.json"), nil
}

func loadPreferences() (preferences, error) {
	path, err := preferencesPath()
	if err != nil {
		return defaultPreferences(), err
	}
	return loadPreferencesFrom(path)
}

func loadPreferencesFrom(path string) (preferences, error) {
	prefs := defaultPreferences()
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return prefs, nil
	}
	if err != nil {
		return prefs, fmt.Errorf("read preferences: %w", err)
	}
	if err := json.Unmarshal(data, &prefs); err != nil {
		return defaultPreferences(), fmt.Errorf("parse preferences: %w", err)
	}
	return prefs.normalized(), nil
}

func savePreferences(prefs preferences) error {
	path, err := preferencesPath()
	if err != nil {
		return err
	}
	return savePreferencesTo(path, prefs)
}

func savePreferencesTo(path string, prefs preferences) error {
	prefs = prefs.normalized()
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return fmt.Errorf("create preferences directory: %w", err)
	}

	tmp, err := os.CreateTemp(filepath.Dir(path), ".preferences-*.tmp")
	if err != nil {
		return fmt.Errorf("create temporary preferences: %w", err)
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
		return fmt.Errorf("set preferences permissions: %w", err)
	}
	enc := json.NewEncoder(tmp)
	enc.SetIndent("", "  ")
	if err := enc.Encode(prefs); err != nil {
		return fmt.Errorf("write preferences: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("close preferences: %w", err)
	}
	if err := os.Rename(tmpName, path); err != nil {
		return fmt.Errorf("replace preferences: %w", err)
	}
	ok = true
	return nil
}

func sortedSessions(sessions []tmux.Session, prefs preferences) []tmux.Session {
	result := append([]tmux.Session(nil), sessions...)
	prefs = prefs.normalized()

	sort.SliceStable(result, func(i, j int) bool {
		a, b := result[i], result[j]
		switch prefs.Sort {
		case sortName:
			return sessionNameLess(a.Name, b.Name)
		case sortOrder:
			aOrder, aOrdered := prefs.Orders[a.Name]
			bOrder, bOrdered := prefs.Orders[b.Name]
			if aOrdered != bOrdered {
				return aOrdered
			}
			if aOrdered && aOrder != bOrder {
				return aOrder < bOrder
			}
			return sessionNameLess(a.Name, b.Name)
		default:
			if !a.Activity.Equal(b.Activity) {
				return a.Activity.After(b.Activity)
			}
			return sessionNameLess(a.Name, b.Name)
		}
	})
	return result
}

func sessionNameLess(a, b string) bool {
	aLower, bLower := strings.ToLower(a), strings.ToLower(b)
	if aLower == bLower {
		return a < b
	}
	return aLower < bLower
}
