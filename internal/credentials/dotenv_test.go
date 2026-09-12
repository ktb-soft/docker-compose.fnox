package credentials

import (
	"maps"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestParse(t *testing.T) {
	content := `
# a comment
OP_SERVICE_ACCOUNT_TOKEN=ops_abc123
export AGE_KEY=AGE-SECRET-KEY-1

QUOTED='literal $notexpanded'
ESCAPED="line\nbreak"
TRAILING=value # not part of it
HASH=abc#notacomment
`

	want := map[string]string{
		"OP_SERVICE_ACCOUNT_TOKEN": "ops_abc123",
		"AGE_KEY":                  "AGE-SECRET-KEY-1",
		"QUOTED":                   "literal $notexpanded",
		"ESCAPED":                  "line\nbreak",
		"TRAILING":                 "value",
		"HASH":                     "abc#notacomment",
	}

	got, err := Parse(content)
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if !maps.Equal(got, want) {
		t.Errorf("Parse() = %v, want %v", got, want)
	}
}

func TestParseRejects(t *testing.T) {
	tests := []struct {
		name    string
		content string
		want    string
	}{
		{"no equals", "JUST_A_KEY", "line 1: expected KEY=value"},
		{"bad key", "2FA=x", "line 1"},
		{"unterminated", `K="open`, "line 1: unterminated double quote"},
		{"reports the line", "A=1\n\n# c\nB", "line 4"},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			_, err := Parse(test.content)
			if err == nil {
				t.Fatalf("Parse(%q) succeeded, want error", test.content)
			}
			if !strings.Contains(err.Error(), test.want) {
				t.Errorf("error = %q, want it to mention %q", err, test.want)
			}
		})
	}
}

func TestApplyLetsTheHostEnvironmentWin(t *testing.T) {
	path := filepath.Join(t.TempDir(), "creds.env")
	content := "ALREADY_SET=from_file\nFRESH=from_file\n"
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	t.Setenv("ALREADY_SET", "from_host")
	t.Setenv("FRESH", "")
	os.Unsetenv("FRESH")

	if err := Apply(path); err != nil {
		t.Fatalf("Apply: %v", err)
	}

	if got := os.Getenv("ALREADY_SET"); got != "from_host" {
		t.Errorf("ALREADY_SET = %q, want the host value to survive", got)
	}
	if got := os.Getenv("FRESH"); got != "from_file" {
		t.Errorf("FRESH = %q, want %q", got, "from_file")
	}
}

func TestApplyEmptyPathIsANoop(t *testing.T) {
	if err := Apply(""); err != nil {
		t.Errorf("Apply(\"\") = %v, want nil", err)
	}
}

func TestApplyMissingFile(t *testing.T) {
	err := Apply(filepath.Join(t.TempDir(), "absent.env"))
	if err == nil {
		t.Fatal("Apply succeeded on a missing file, want error")
	}
	if !strings.Contains(err.Error(), "reading credentials file") {
		t.Errorf("error = %q, want it to say which step failed", err)
	}
}
