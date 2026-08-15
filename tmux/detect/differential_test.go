package detect

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"testing"
)

// The rules in manifests/ are herdr's, and so is the engine they were written
// against. This package reimplements that engine in Go, which means every
// behavioural difference — a region sliced one line short, a `contains` that
// forgot to fold case, a regex whose \s stopped matching a no-break space —
// shows up as a wrong badge and nothing else.
//
// So: run both engines over the same screens and compare. herdr can classify a
// saved fixture without a server:
//
//	herdr agent explain --file PATH --agent LABEL --json
//
// Point HERDR_BIN at a herdr binary to run this. It is skipped otherwise,
// because CI has no herdr, but it should be run whenever the manifests are
// re-synced.
//
//	HERDR_BIN=~/dev/projects/herdr/target/debug/herdr \
//	  go test ./tmux/detect/ -run Differential -v -timeout 30m

func TestDifferentialAgainstHerdrEngine(t *testing.T) {
	herdrBin := os.Getenv("HERDR_BIN")
	if herdrBin == "" {
		t.Skip("set HERDR_BIN to a herdr binary to run the differential test")
	}

	manifests, err := loadBundledManifests()
	if err != nil {
		t.Fatalf("load manifests: %v", err)
	}

	fixtures := buildFixtures(t, manifests)
	if len(fixtures) == 0 {
		t.Fatal("no fixtures generated")
	}

	agents := make([]string, 0, len(manifests))
	for id := range manifests {
		agents = append(agents, id)
	}
	sort.Strings(agents)

	// Each fixture is compared against the agent it was built from — the
	// positive direction — plus a rotating handful of others, which is the
	// negative direction: agent A's screen must not light up agent B's rules.
	// The full cross product is ~3,600 herdr invocations and does not finish
	// inside a test timeout; this samples it while still touching every agent.
	type pair struct {
		fixture fixture
		agent   string
	}
	var pairs []pair
	for index, fixture := range fixtures {
		pairs = append(pairs, pair{fixture, fixture.agent})
		for offset := 1; offset <= 3; offset++ {
			pairs = append(pairs, pair{fixture, agents[(index+offset)%len(agents)]})
		}
	}

	type result struct {
		pair
		got, want string
		compared  bool
	}

	// herdr is a separate process per comparison, so the wall clock is bound by
	// process spawn, not by either engine.
	const workers = 8
	jobs := make(chan pair)
	results := make(chan result, len(pairs))
	var wait sync.WaitGroup
	for range workers {
		wait.Add(1)
		go func() {
			defer wait.Done()
			for job := range jobs {
				want, ok := herdrState(herdrBin, job.fixture.path, job.agent)
				if !ok {
					results <- result{pair: job}
					continue
				}
				got := Detect(job.agent, Input{Screen: job.fixture.screen}).State.String()
				results <- result{pair: job, got: got, want: want, compared: true}
			}
		}()
	}
	go func() {
		for _, job := range pairs {
			jobs <- job
		}
		close(jobs)
	}()
	go func() {
		wait.Wait()
		close(results)
	}()

	compared, mismatched := 0, 0
	for outcome := range results {
		if !outcome.compared {
			continue
		}
		compared++
		if outcome.got != outcome.want {
			mismatched++
			t.Errorf("fixture %s as %s: go=%s herdr=%s",
				outcome.fixture.name, outcome.agent, outcome.got, outcome.want)
		}
	}
	t.Logf("compared %d (fixture, agent) pairs, %d mismatched", compared, mismatched)
}

type fixture struct {
	name   string
	agent  string
	path   string
	screen string
}

// buildFixtures makes one screen per manifest out of the literal phrases that
// manifest's rules look for.
//
// These are not realistic screens. They are dense with trigger phrases on
// purpose, so that running every fixture against every agent exercises far more
// rules — including the negative direction, where agent A's screen must *not*
// light up agent B's rules.
func buildFixtures(t *testing.T, manifests map[string]Manifest) []fixture {
	t.Helper()
	dir := t.TempDir()

	names := make([]string, 0, len(manifests))
	for id := range manifests {
		names = append(names, id)
	}
	sort.Strings(names)

	fixtures := make([]fixture, 0, len(names)*2)
	for _, id := range names {
		literals := collectContains(manifests[id])
		if len(literals) == 0 {
			continue
		}

		// One fixture with every phrase, and one per phrase, so a rule that
		// only fires in isolation is still reached.
		variants := map[string]string{
			id + "-all": strings.Join(literals, "\n") + "\n",
		}
		for index, literal := range literals {
			if index >= 8 {
				break
			}
			variants[id+"-"+sanitize(literal)] = "some earlier output\n" + literal + "\n"
		}

		for name, screen := range variants {
			path := filepath.Join(dir, name+".txt")
			if err := os.WriteFile(path, []byte(screen), 0o600); err != nil {
				t.Fatalf("write fixture: %v", err)
			}
			fixtures = append(fixtures, fixture{name: name, agent: id, path: path, screen: screen})
		}
	}

	sort.Slice(fixtures, func(i, j int) bool { return fixtures[i].name < fixtures[j].name })
	return fixtures
}

func collectContains(manifest Manifest) []string {
	seen := map[string]bool{}
	var literals []string
	for _, rule := range manifest.Rules {
		walkGates(rule.Gate(), func(gate Gate) {
			for _, needle := range gate.Contains {
				if needle == "" || seen[needle] {
					continue
				}
				seen[needle] = true
				literals = append(literals, needle)
			}
		})
	}
	sort.Strings(literals)
	return literals
}

func sanitize(value string) string {
	var out strings.Builder
	for _, ch := range value {
		switch {
		case ch >= 'a' && ch <= 'z', ch >= '0' && ch <= '9':
			out.WriteRune(ch)
		case ch >= 'A' && ch <= 'Z':
			out.WriteRune(ch + 32)
		default:
			out.WriteByte('-')
		}
	}
	trimmed := strings.Trim(out.String(), "-")
	if len(trimmed) > 32 {
		trimmed = trimmed[:32]
	}
	if trimmed == "" {
		return "x"
	}
	return trimmed
}

// herdrState asks herdr to classify a fixture. Returns false when herdr cannot
// answer for that agent at all, which is not a mismatch.
func herdrState(bin, path, agent string) (string, bool) {
	cmd := exec.Command(bin, "agent", "explain", "--file", path, "--agent", agent, "--json")
	cmd.Env = append(os.Environ(), "HERDR_SOCKET_PATH=", "HERDR_CLIENT_SOCKET_PATH=")
	output, err := cmd.Output()
	if err != nil {
		return "", false
	}

	var explained struct {
		State string `json:"state"`
	}
	if err := json.Unmarshal(output, &explained); err != nil {
		return "", false
	}
	if explained.State == "" {
		return "", false
	}
	return explained.State, true
}
