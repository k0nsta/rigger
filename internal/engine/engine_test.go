package engine

import (
	"context"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"

	"github.com/k0nsta/rigger/internal/action"
	"github.com/k0nsta/rigger/internal/engine/mocks"
	"github.com/k0nsta/rigger/internal/manifest"
	"github.com/k0nsta/rigger/internal/sh"
	"github.com/k0nsta/rigger/internal/state"
	"github.com/k0nsta/rigger/internal/testutil"
)

// fakeShell answers scripts by prefix; anything unknown fails the test.
type fakeShell struct {
	t       *testing.T
	answers map[string]sh.Result
	ran     []string
}

func (f *fakeShell) run(_ context.Context, c sh.Cmd) (sh.Result, error) {
	f.ran = append(f.ran, c.String())
	for prefix, res := range f.answers {
		if strings.HasPrefix(c.String(), prefix) {
			if c.OnLine != nil && res.Output != "" {
				for _, l := range sh.Lines(res.Output) {
					c.OnLine(l)
				}
			}
			return res, nil
		}
	}
	f.t.Errorf("unexpected command: %s", c.String())
	return sh.Result{Code: 127}, nil
}

// newEngine builds an engine over a temp repo and HOME with a fake shell.
func newEngine(t *testing.T, manifestYAML string, files map[string]string, answers map[string]sh.Result) (*Engine, *fakeShell) {
	t.Helper()
	files["manifest.yaml"] = manifestYAML
	repo := testutil.Tree(t, files)
	m, err := manifest.Load(repo)
	require.NoError(t, err)
	secrets, err := action.LoadSecrets(filepath.Join(t.TempDir(), "secrets.env"), nil)
	require.NoError(t, err)

	fs := &fakeShell{t: t, answers: answers}
	r := mocks.NewMockRunner(gomock.NewController(t))
	r.EXPECT().Run(gomock.Any(), gomock.Any()).DoAndReturn(fs.run).AnyTimes()
	home := t.TempDir()
	return &Engine{
		Manifest: m,
		Env:      &action.Env{Home: home, Repo: repo, BackupRoot: filepath.Join(home, "backups")},
		Runner:   r,
		Secrets:  secrets,
		Local:    &state.Local{Seeds: map[string]string{}},
		Platform: manifest.Darwin,
		Host:     "mac",
		AppPaths: action.DefaultAppPaths(home),
	}, fs
}

// plan resolves picked and probes it.
func plan(t *testing.T, e *Engine, picked ...string) []ComponentStatus {
	t.Helper()
	res, err := e.Manifest.Resolve(e.Platform, picked)
	require.NoError(t, err)
	statuses, err := e.Probe(context.Background(), res, nil)
	require.NoError(t, err)
	return statuses
}

const chainManifest = `
version: 1
components:
  - {id: base, group: g, cmds: [{name: base, check: "check-base", run: "run-base"}]}
  - {id: dep, group: g, requires: [base], links: [{src: f, dst: ~/.f}]}
  - {id: solo, group: g, cmds: [{name: solo, check: "check-solo", run: "run-solo"}]}
`

func TestProbeAggregationAndAddedBecause(t *testing.T) {
	e, _ := newEngine(t, `
version: 1
components:
  - {id: base, group: g, cmds: [{check: "check-base", run: "run-base"}]}
  - id: cfg
    group: g
    requires: [base]
    links: [{src: f, dst: ~/.f}]
    seeds: [{src: s, dst: ~/.s}]
`, map[string]string{"f": "x", "s": "template"}, map[string]sh.Result{"check-base": {Code: 0}})
	testutil.WriteFile(t, filepath.Join(e.Env.Home, ".s"), "someone else's")

	statuses := plan(t, e, "cfg")
	require.Len(t, statuses, 2)
	assert.Equal(t, "base", statuses[0].ID())
	assert.Equal(t, "cfg", statuses[0].AddedBecause)
	assert.True(t, statuses[0].Settled())
	assert.Equal(t, "ok", statuses[0].Summary)

	cfg := statuses[1]
	assert.Equal(t, action.BothChanged, cfg.State, "worst action wins")
	assert.Contains(t, cfg.Summary, "~/.f missing")
	assert.Contains(t, cfg.Summary, "~/.s both-changed")
}

func TestApplyOrderAndFailureIsolation(t *testing.T) {
	e, fs := newEngine(t, chainManifest, map[string]string{"f": "x"}, map[string]sh.Result{
		"check-base": {Code: 1},
		"run-base":   {Code: 2, Output: "boom"},
		"check-solo": {Code: 1},
		"run-solo":   {Code: 0, Output: "solo ran"},
	})
	statuses := plan(t, e, "dep", "solo")

	var events []string
	outcomes := e.Apply(context.Background(), statuses, Decisions{}, func(ev Event) {
		events = append(events, string(ev.Kind)+" "+ev.Component+" "+ev.Text)
	})

	assert.Equal(t, []Outcome{
		{Component: "base", Kind: Failed, Err: outcomes[0].Err},
		{Component: "dep", Kind: Skipped, Err: outcomes[1].Err},
		{Component: "solo", Kind: Done},
	}, outcomes)
	require.ErrorContains(t, outcomes[0].Err, "base failed (exit 2)")
	require.ErrorContains(t, outcomes[1].Err, "needs base")
	assert.Contains(t, events, "line solo solo ran")
	assert.NotContains(t, fs.ran, "link")
	assert.NoFileExists(t, filepath.Join(e.Env.Home, ".f"))
}

func TestApplyLinksSeedsAndDecisions(t *testing.T) {
	e, _ := newEngine(t, `
version: 1
components:
  - id: cfg
    group: g
    links: [{src: rc, dst: ~/.rc}, {src: other, dst: ~/.other}]
    seeds: [{src: seed, dst: ~/.seed}]
`, map[string]string{"rc": "repo rc", "other": "repo other", "seed": "template"}, map[string]sh.Result{})
	home := e.Env.Home
	testutil.WriteFile(t, filepath.Join(home, ".rc"), "live rc")
	testutil.WriteFile(t, filepath.Join(home, ".other"), "live other")
	testutil.WriteFile(t, filepath.Join(home, ".seed"), "live seed")

	statuses := plan(t, e, "cfg")
	outcomes := e.Apply(context.Background(), statuses, Decisions{
		Adopt:     map[string]bool{"link:~/.rc": true},
		Overwrite: map[string]bool{"seed:~/.seed": true},
	}, nil)
	require.Equal(t, Done, outcomes[0].Kind, outcomes[0].Err)

	assert.Equal(t, "live rc", testutil.ReadFile(t, filepath.Join(e.Env.Repo, "rc")), "adopted")
	assert.Equal(t, "repo other", testutil.ReadFile(t, filepath.Join(home, ".other")), "kept repo")
	assert.Equal(t, "template", testutil.ReadFile(t, filepath.Join(home, ".seed")), "overwritten")
	assert.NotEmpty(t, e.Local.Seeds[filepath.Join(home, ".seed")], "render hash remembered")

	again := plan(t, e, "cfg")
	assert.True(t, again[0].Settled(), again[0].Summary)
}

func TestApplySkipsComponentsWaitingForSecrets(t *testing.T) {
	e, _ := newEngine(t, `
version: 1
components:
  - {id: tool, group: g, seeds: [{src: t, dst: ~/.t, secrets: [TOKEN]}]}
`, map[string]string{"t": "${TOKEN}"}, map[string]sh.Result{})
	statuses := plan(t, e, "tool")
	assert.Equal(t, []string{"TOKEN"}, MissingSecrets(statuses, e.Secrets))
	outcomes := e.Apply(context.Background(), statuses, Decisions{}, nil)
	assert.Equal(t, Skipped, outcomes[0].Kind)
	require.ErrorContains(t, outcomes[0].Err, "waiting for a secret")
}

func TestBrewWithoutHomebrew(t *testing.T) {
	e, _ := newEngine(t, `
version: 1
components:
  - {id: pkgs, group: g, brewfile: Brewfile}
`, map[string]string{"Brewfile": "brew \"git\"\ncask \"slack\"\nbrew \"jq\"\n"}, map[string]sh.Result{
		"command -v brew": {Code: 1},
	})
	res, err := e.Manifest.Resolve(e.Platform, []string{"pkgs"})
	require.NoError(t, err)
	statuses, err := e.Probe(context.Background(), res, map[string]bool{"jq": true})
	require.NoError(t, err)
	assert.Equal(t, "2 to install (Homebrew not installed yet)", statuses[0].Summary)
	p, ok := statuses[0].Actions[0].Brew()
	require.True(t, ok)
	assert.Len(t, p.Excluded, 1)
	assert.True(t, NeedsSudo(statuses), "a cask is missing")
}

func TestBrewInventoryLoadedOnce(t *testing.T) {
	e, fs := newEngine(t, `
version: 1
components:
  - {id: a, group: g, brewfile: A}
  - {id: b, group: g, brewfile: B}
`, map[string]string{"A": "brew \"git\"\n", "B": "brew \"jq\"\n"}, map[string]sh.Result{
		"command -v brew":     {Code: 0},
		"brew list --formula": {Output: "git\njq\n"},
		"brew list --cask":    {Output: ""},
		"brew tap":            {Output: ""},
	})
	statuses := plan(t, e, "a", "b")
	assert.True(t, statuses[0].Settled())
	assert.True(t, statuses[1].Settled())
	count := 0
	for _, c := range fs.ran {
		if c == "brew list --formula -1" {
			count++
		}
	}
	assert.Equal(t, 1, count)
	assert.False(t, NeedsSudo(statuses))
}

func TestNeedsSudoForSudoCmdAndApt(t *testing.T) {
	e, _ := newEngine(t, `
version: 1
components:
  - {id: s, group: g, cmds: [{check: "check-s", run: "run-s", sudo: true}]}
`, map[string]string{}, map[string]sh.Result{"check-s": {Code: 1}})
	assert.True(t, NeedsSudo(plan(t, e, "s")))
}
