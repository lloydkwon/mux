package ui

import (
	"testing"

	tea "github.com/charmbracelet/bubbletea"
)

// isolatePreferences keeps answerSetupOffer's savePreferences out of the
// developer's real config directory.
func isolatePreferences(t *testing.T) {
	t.Helper()
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
}

func setupOfferModel() Model {
	m := NewModel()
	m.mode = modeSetupOffer
	m.width, m.height = 100, 30
	return m
}

func TestSetupOfferAcceptInstallsAndIsNotAskedAgain(t *testing.T) {
	isolatePreferences(t)
	m := setupOfferModel()

	next, cmd := m.updateSetupOffer(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'y'}})
	got := next.(Model)
	if got.mode != modeList {
		t.Errorf("mode = %v, want modeList", got.mode)
	}
	if cmd == nil {
		t.Fatal("accepting must return the install command")
	}
	if !got.prefs.SetupOffered {
		t.Error("accepting must record the offer as answered")
	}
}

func TestSetupOfferDeclineInstallsNothing(t *testing.T) {
	isolatePreferences(t)
	m := setupOfferModel()

	next, cmd := m.updateSetupOffer(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'n'}})
	got := next.(Model)
	if got.mode != modeList {
		t.Errorf("mode = %v, want modeList", got.mode)
	}
	if cmd != nil {
		t.Error("declining must not run the install")
	}
	if !got.prefs.SetupOffered {
		t.Error("declining is still an answer — it must not be asked again")
	}

	// And the answer survives a reload, which is the whole point of saving it.
	prefs, err := loadPreferences()
	if err != nil {
		t.Fatalf("loadPreferences: %v", err)
	}
	if !prefs.SetupOffered {
		t.Error("the recorded answer did not reach disk")
	}
}

// The 500ms tick flows through Update while the offer is up; only a key may
// answer the question.
func TestSetupOfferSurvivesTicks(t *testing.T) {
	isolatePreferences(t)
	m := setupOfferModel()

	next, _ := m.updateSetupOffer(tickMsg{})
	if got := next.(Model); got.mode != modeSetupOffer {
		t.Errorf("mode = %v after a tick, want the offer still up", got.mode)
	}
}

func TestOfferSetupIfNeededRespectsARecordedAnswer(t *testing.T) {
	isolatePreferences(t)
	m := NewModel()
	m.prefs.SetupOffered = true

	if got := m.OfferSetupIfNeeded(); got.mode != modeList {
		t.Errorf("mode = %v, want modeList when the offer was already answered", got.mode)
	}
}

func TestOfferSetupIfNeededOffersOnAFreshMachine(t *testing.T) {
	isolatePreferences(t)
	t.Setenv("HOME", t.TempDir()) // no tmux config anywhere → nothing installed

	m := NewModel()
	if got := m.OfferSetupIfNeeded(); got.mode != modeSetupOffer {
		t.Errorf("mode = %v, want the offer on a machine with no mux region", got.mode)
	}
}
