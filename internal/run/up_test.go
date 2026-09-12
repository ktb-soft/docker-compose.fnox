package run

import (
	"bytes"
	"context"
	"encoding/json"
	"slices"
	"strings"
	"testing"

	compose "github.com/ktb-soft/go.compose-provider"

	"github.com/ktb-soft/docker-compose.fnox/internal/fnox"
)

// stubSecrets stands in for fnox. It records what it was asked for and returns
// what the test wants, so no binary is ever executed.
type stubSecrets struct {
	secrets   map[string]string
	exportErr error
	syncErr   error

	configFiles []string
	configErr   error

	synced   bool
	syncOpts fnox.Options
	exported bool
}

func (s *stubSecrets) ConfigFiles(_ context.Context, opts fnox.Options) ([]string, error) {
	if s.configFiles == nil && s.configErr == nil {
		// Contained by default: the only config loaded is the one asked for.
		return []string{opts.ConfigPath}, nil
	}
	return s.configFiles, s.configErr
}

func (s *stubSecrets) Sync(_ context.Context, opts fnox.Options) error {
	s.synced = true
	s.syncOpts = opts
	return s.syncErr
}

func (s *stubSecrets) Export(_ context.Context, _ fnox.Options) (map[string]string, error) {
	s.exported = true
	return s.secrets, s.exportErr
}

// provider wires the handler the way main.go does, so the tests exercise the
// same option parsing, defaulting and validation Compose will.
func provider(secrets Secrets) compose.Provider {
	return compose.Provider{
		Name:     "docker-compose-fnox",
		Up:       Up(secrets),
		UpParams: fnox.Params,
	}
}

// runUp drives one up invocation and returns the protocol lines it wrote.
func runUp(t *testing.T, secrets Secrets, options ...string) ([]message, error) {
	t.Helper()

	args := append([]string{"compose", "--project-name", "stack", "up"}, options...)
	args = append(args, "secrets")

	var out bytes.Buffer
	err := provider(secrets).Run(context.Background(), args, &out)
	return decode(t, out.String()), err
}

type message struct {
	Type    string `json:"type"`
	Message string `json:"message"`
}

func decode(t *testing.T, output string) []message {
	t.Helper()

	var messages []message
	for _, line := range strings.Split(strings.TrimSpace(output), "\n") {
		if line == "" {
			continue
		}
		var m message
		if err := json.Unmarshal([]byte(line), &m); err != nil {
			t.Fatalf("decoding %q: %v", line, err)
		}
		messages = append(messages, m)
	}
	return messages
}

// only returns the message bodies of one type, in the order they were written.
func only(messages []message, kind string) []string {
	var bodies []string
	for _, m := range messages {
		if m.Type == kind {
			bodies = append(bodies, m.Message)
		}
	}
	return bodies
}

func TestUpInjectsEverySecretUnprefixed(t *testing.T) {
	secrets := &stubSecrets{secrets: map[string]string{
		"DATABASE_URL": "postgres://example",
		"API_TOKEN":    "not-a-real-token",
	}}

	messages, err := runUp(t, secrets, "--path_config_toml=/srv/fnox.toml")
	if err != nil {
		t.Fatalf("Run: %v", err)
	}

	// Sorted, so a repeated up emits an identical stream.
	want := []string{
		"API_TOKEN=not-a-real-token",
		"DATABASE_URL=postgres://example",
	}
	if got := only(messages, "rawsetenv"); !slices.Equal(got, want) {
		t.Errorf("rawsetenv = %v, want %v", got, want)
	}
	if secrets.synced {
		t.Error("synced without cache_enabled")
	}
}

func TestUpReportsTheNamesItInjected(t *testing.T) {
	secrets := &stubSecrets{secrets: map[string]string{"API_TOKEN": "x"}}

	messages, err := runUp(t, secrets, "--path_config_toml=/srv/fnox.toml")
	if err != nil {
		t.Fatalf("Run: %v", err)
	}

	info := strings.Join(only(messages, "info"), "\n")
	if !strings.Contains(info, "1 secrets injected: API_TOKEN") {
		t.Errorf("info = %q, want the count and the names", info)
	}
}

func TestUpEmptyExport(t *testing.T) {
	messages, err := runUp(t, &stubSecrets{secrets: map[string]string{}},
		"--path_config_toml=/srv/fnox.toml")
	if err != nil {
		t.Fatalf("Run: %v", err)
	}

	if got := only(messages, "rawsetenv"); len(got) != 0 {
		t.Errorf("rawsetenv = %v, want none", got)
	}
	if info := strings.Join(only(messages, "info"), "\n"); !strings.Contains(info, "0 secrets injected") {
		t.Errorf("info = %q, want it to say nothing was injected", info)
	}
}

// A secret value belongs in exactly one place: the rawsetenv line. An info or
// error message carrying one would land in the user's terminal and in whatever
// collects Compose output.
func TestUpNeverWritesValuesOutsideRawSetEnv(t *testing.T) {
	const value = "s3cr3t-value-do-not-log"
	secrets := &stubSecrets{secrets: map[string]string{"API_TOKEN": value}}

	messages, err := runUp(t, secrets, "--path_config_toml=/srv/fnox.toml")
	if err != nil {
		t.Fatalf("Run: %v", err)
	}

	for _, m := range messages {
		if m.Type == "rawsetenv" {
			continue
		}
		if strings.Contains(m.Message, value) {
			t.Errorf("%s message leaked a secret value: %q", m.Type, m.Message)
		}
	}
}

func TestUpCachesBeforeExporting(t *testing.T) {
	secrets := &stubSecrets{secrets: map[string]string{"API_TOKEN": "x"}}

	_, err := runUp(t, secrets,
		"--path_config_toml=/srv/fnox.toml",
		"--cache_enabled=true",
		"--cache_provider=sync-age",
		"--cache_use_local_file=true",
	)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}

	if !secrets.synced {
		t.Fatal("cache_enabled did not sync")
	}
	if secrets.syncOpts.CacheProvider != "sync-age" || !secrets.syncOpts.CacheLocalFile {
		t.Errorf("sync options = %+v, want the cache settings passed through", secrets.syncOpts)
	}
}

// A failed sync means the cached copies are stale or absent. Exporting anyway
// would either serve old values or reach the backend the cache exists to
// avoid, so the up fails instead.
func TestUpStopsWhenSyncFails(t *testing.T) {
	secrets := &stubSecrets{syncErr: errFake}

	_, err := runUp(t, secrets,
		"--path_config_toml=/srv/fnox.toml",
		"--cache_enabled=true",
		"--cache_provider=sync-age",
	)
	if err == nil {
		t.Fatal("Run succeeded, want the sync error")
	}
	if secrets.exported {
		t.Error("exported after the sync failed")
	}
}

func TestUpRejectsAKeyThatIsNotAnEnvName(t *testing.T) {
	secrets := &stubSecrets{secrets: map[string]string{
		"GOOD":      "x",
		"not-valid": "y",
	}}

	messages, err := runUp(t, secrets, "--path_config_toml=/srv/fnox.toml")
	if err == nil {
		t.Fatal("Run succeeded, want the invalid key rejected")
	}
	// Nothing is emitted, so a broken config cannot half populate a service.
	if got := only(messages, "rawsetenv"); len(got) != 0 {
		t.Errorf("rawsetenv = %v, want none before the failure", got)
	}
}

func TestUpRejectsAnUnknownOption(t *testing.T) {
	_, err := runUp(t, &stubSecrets{},
		"--path_config_toml=/srv/fnox.toml", "--nonsense=1")
	if err == nil {
		t.Fatal("Run succeeded, want the unknown option rejected")
	}
	if !strings.Contains(err.Error(), "nonsense") {
		t.Errorf("error = %q, want it to name the option", err)
	}
}

var errFake = fakeError("fnox exploded")

type fakeError string

func (e fakeError) Error() string { return string(e) }

// fnox merges every fnox.toml in the directories above its working directory,
// so a config that names two secrets can inject twenty. The up fails rather
// than shipping them.
func TestUpRefusesWhenAParentConfigWouldBeMerged(t *testing.T) {
	secrets := &stubSecrets{
		secrets: map[string]string{"API_TOKEN": "x"},
		configFiles: []string{
			"/srv/stack/fnox.toml",
			"/srv/fnox.toml",
			"/home/operator/.config/fnox/config.toml",
		},
	}

	_, err := runUp(t, secrets, "--path_config_toml=/srv/stack/fnox.toml")
	if err == nil {
		t.Fatal("Run succeeded, want the parent config refused")
	}
	if !strings.Contains(err.Error(), "/srv/fnox.toml") {
		t.Errorf("error = %q, want it to name the parent config", err)
	}
	if !strings.Contains(err.Error(), "root = true") {
		t.Errorf("error = %q, want it to say how to fix it", err)
	}
	if secrets.exported {
		t.Error("exported anyway")
	}
}

// The user's global config lives outside the project tree, holds the shared
// provider definitions, and is not a surprise inherited from where the stack
// sits on disk.
func TestUpAllowsTheGlobalConfig(t *testing.T) {
	secrets := &stubSecrets{
		secrets: map[string]string{"API_TOKEN": "x"},
		configFiles: []string{
			"/srv/stack/fnox.toml",
			"/home/operator/.config/fnox/config.toml",
		},
	}

	if _, err := runUp(t, secrets, "--path_config_toml=/srv/stack/fnox.toml"); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if !secrets.exported {
		t.Error("did not export")
	}
}
