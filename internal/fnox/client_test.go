package fnox

import (
	"context"
	"errors"
	"strings"
	"testing"
)

// fakeRunner returns canned output instead of running fnox.
type fakeRunner struct {
	stdout []byte
	err    error

	dir  string
	args []string
}

func (f *fakeRunner) Run(_ context.Context, dir string, args []string) ([]byte, error) {
	f.dir = dir
	f.args = args
	return f.stdout, f.err
}

func TestExportParsesTheSecretsAndDropsTheMetadata(t *testing.T) {
	runner := &fakeRunner{stdout: []byte(`{
	  "secrets": {"API_TOKEN": "x", "DATABASE_URL": "postgres://example"},
	  "metadata": {"exported_at": "2026-09-11T00:00:00Z", "total_secrets": 2}
	}`)}

	secrets, err := Client{Runner: runner}.Export(context.Background(),
		Options{ConfigPath: "/srv/stack/fnox.toml"})
	if err != nil {
		t.Fatalf("Export: %v", err)
	}

	if len(secrets) != 2 || secrets["API_TOKEN"] != "x" {
		t.Errorf("Export() = %v, want both secrets", secrets)
	}
	// fnox runs beside its config, so a relative key_file in that config
	// resolves the way it does for a human running fnox in that directory.
	if runner.dir != "/srv/stack" {
		t.Errorf("ran in %q, want the config's directory", runner.dir)
	}
}

func TestExportOnEmptyConfig(t *testing.T) {
	runner := &fakeRunner{stdout: []byte(`{"secrets": {}}`)}

	secrets, err := Client{Runner: runner}.Export(context.Background(),
		Options{ConfigPath: "/srv/fnox.toml"})
	if err != nil {
		t.Fatalf("Export: %v", err)
	}
	if secrets == nil || len(secrets) != 0 {
		t.Errorf("Export() = %v, want an empty map rather than nil", secrets)
	}
}

func TestExportRejectsOutputThatIsNotJSON(t *testing.T) {
	runner := &fakeRunner{stdout: []byte("fnox: +2 A, B\n")}

	_, err := Client{Runner: runner}.Export(context.Background(),
		Options{ConfigPath: "/srv/fnox.toml"})
	if err == nil {
		t.Fatal("Export succeeded on non-JSON output, want error")
	}
	if !strings.Contains(err.Error(), "parsing") {
		t.Errorf("error = %q, want it to name the step that failed", err)
	}
}

func TestExportFailsWhenFnoxFails(t *testing.T) {
	runner := &fakeRunner{err: errors.New("exit status 1")}

	if _, err := (Client{Runner: runner}).Export(context.Background(),
		Options{ConfigPath: "/srv/fnox.toml"}); err == nil {
		t.Fatal("Export succeeded, want the runner error surfaced")
	}
}

// fnox colours its errors even under --no-color. Raw escape sequences inside a
// JSON protocol message reach the user as literal noise.
func TestDiagnosticStripsColour(t *testing.T) {
	stderr := "Error: \x1b[31mfnox::config::read_failed\x1b[0m \x1b[31m×\x1b[0m No such file"

	got := diagnostic(stderr)
	if strings.Contains(got, "\x1b") {
		t.Errorf("diagnostic() = %q, want no escape sequences", got)
	}
	if !strings.Contains(got, "fnox::config::read_failed") {
		t.Errorf("diagnostic() = %q, want the error text kept", got)
	}
}

func TestDiagnosticOnSilence(t *testing.T) {
	if got := diagnostic("  \n "); got != "" {
		t.Errorf("diagnostic() = %q, want empty", got)
	}
}
