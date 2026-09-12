// Package credentials loads backend credentials for fnox from a dotenv file.
//
// Credentials never travel through the Compose file itself: provider.options
// become process arguments, visible in the process table and printed by
// `docker compose config`. The option holds a path; the file holds the secret.
package credentials

import (
	"fmt"
	"os"
	"regexp"
	"strings"
)

var keyPattern = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)

var escapes = strings.NewReplacer(`\n`, "\n", `\r`, "\r", `\t`, "\t", `\"`, `"`, `\\`, `\`)

// Apply exports every variable in the dotenv file at path into this process,
// where fnox will inherit it.
//
// The host environment wins: a variable already exported by the shell running
// docker compose is left alone, so a file cannot silently override a token set
// for a one-off run. An empty path is a no-op.
//
// The file is read exactly once. Some credential paths are FIFOs rather than
// regular files, and a second read of a FIFO blocks forever.
func Apply(path string) error {
	if path == "" {
		return nil
	}

	content, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("reading credentials file: %w", err)
	}

	values, err := Parse(string(content))
	if err != nil {
		return fmt.Errorf("%s: %w", path, err)
	}

	for key, value := range values {
		if _, set := os.LookupEnv(key); set {
			continue
		}
		if err := os.Setenv(key, value); err != nil {
			return fmt.Errorf("setting %s: %w", key, err)
		}
	}
	return nil
}

// Parse reads the dotenv subset `docker compose --env-file` accepts: KEY=value
// per line, # comments, blank lines, an optional export prefix, and single
// quoted (literal) or double quoted (escapes expanded) values.
func Parse(content string) (map[string]string, error) {
	values := make(map[string]string)

	for number, line := range strings.Split(content, "\n") {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" || strings.HasPrefix(trimmed, "#") {
			continue
		}
		trimmed = strings.TrimPrefix(trimmed, "export ")

		key, raw, found := strings.Cut(trimmed, "=")
		if !found {
			return nil, fmt.Errorf("line %d: expected KEY=value", number+1)
		}
		key = strings.TrimSpace(key)
		if !keyPattern.MatchString(key) {
			return nil, fmt.Errorf("line %d: %q is not a valid environment variable name", number+1, key)
		}

		value, err := parseValue(strings.TrimSpace(raw))
		if err != nil {
			return nil, fmt.Errorf("line %d: %w", number+1, err)
		}
		values[key] = value
	}
	return values, nil
}

func parseValue(value string) (string, error) {
	switch {
	case strings.HasPrefix(value, `'`):
		if len(value) < 2 || !strings.HasSuffix(value, `'`) {
			return "", fmt.Errorf("unterminated single quote")
		}
		return value[1 : len(value)-1], nil
	case strings.HasPrefix(value, `"`):
		if len(value) < 2 || !strings.HasSuffix(value, `"`) {
			return "", fmt.Errorf("unterminated double quote")
		}
		return escapes.Replace(value[1 : len(value)-1]), nil
	default:
		return stripComment(value), nil
	}
}

// stripComment removes a trailing comment from an unquoted value. A # only
// starts one when whitespace precedes it, so a value that is itself a fragment
// containing # survives.
func stripComment(value string) string {
	for i, r := range value {
		if r != '#' || i == 0 {
			continue
		}
		if prev := value[i-1]; prev == ' ' || prev == '\t' {
			return strings.TrimSpace(value[:i])
		}
	}
	return value
}
