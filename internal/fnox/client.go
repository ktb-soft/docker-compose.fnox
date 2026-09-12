package fnox

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"regexp"
	"strings"
)

// Binary is the fnox executable the provider shells out to. It must be on the
// PATH of whatever runs docker compose.
const Binary = "fnox"

// Runner executes one fnox command and returns its stdout. Everything that
// touches the operating system lives behind this one method, so the tests can
// replace it with a fake and never run a real binary.
type Runner interface {
	Run(ctx context.Context, dir string, args []string) ([]byte, error)
}

// Client drives fnox through a Runner.
type Client struct {
	Runner Runner
}

// New returns a Client that runs the real fnox binary.
func New() Client {
	return Client{Runner: ExecRunner{}}
}

// exportPayload is the shape of `fnox export --format json`. The metadata
// object that output also carries is deliberately dropped: it holds an export
// timestamp, which would make two otherwise identical ups differ.
type exportPayload struct {
	Secrets map[string]string `json:"secrets"`
}

// Export resolves the secrets a Compose service should receive.
//
// fnox exits non-zero on a missing config, an unknown profile, or a provider
// that cannot decrypt, so a failure here fails the up rather than injecting a
// half populated environment.
func (c Client) Export(ctx context.Context, opts Options) (map[string]string, error) {
	stdout, err := c.Runner.Run(ctx, opts.dir(), opts.exportArgs())
	if err != nil {
		return nil, err
	}

	var payload exportPayload
	if err := json.Unmarshal(stdout, &payload); err != nil {
		return nil, fmt.Errorf("parsing %s export output: %w", Binary, err)
	}
	if payload.Secrets == nil {
		payload.Secrets = map[string]string{}
	}
	return payload.Secrets, nil
}

// ConfigFiles lists every config fnox will actually load for these options, in
// the order it merges them.
//
// fnox searches parent directories and merges what it finds, so the config a
// Compose file names is a starting point rather than a boundary. A stack
// pointed at a project fnox.toml can quietly pick up secrets from the
// operator's home directory. Asking fnox which files are in play is the only
// way to see that before the secrets are injected.
func (c Client) ConfigFiles(ctx context.Context, opts Options) ([]string, error) {
	stdout, err := c.Runner.Run(ctx, opts.dir(), opts.configFilesArgs())
	if err != nil {
		return nil, err
	}

	var files []string
	for _, line := range strings.Split(string(stdout), "\n") {
		if trimmed := strings.TrimSpace(line); trimmed != "" {
			files = append(files, trimmed)
		}
	}
	return files, nil
}

// Sync refreshes the offline cache, writing encrypted copies of each secret
// either into the config itself or into fnox.local.toml beside it.
//
// This writes to disk during an up. It is off unless the Compose file asks
// for it.
func (c Client) Sync(ctx context.Context, opts Options) error {
	_, err := c.Runner.Run(ctx, opts.dir(), opts.syncArgs())
	return err
}

// ExecRunner runs the fnox binary.
type ExecRunner struct{}

// Run executes fnox in dir and returns its stdout.
//
// stdout and stderr are captured separately and never merged. fnox writes its
// shell activation notice and every diagnostic to stderr, so folding the two
// together would put that text in front of the JSON this parses.
func (ExecRunner) Run(ctx context.Context, dir string, args []string) ([]byte, error) {
	binary, err := resolveBinary()
	if err != nil {
		return nil, err
	}

	var stdout, stderr bytes.Buffer
	cmd := exec.CommandContext(ctx, binary, args...)
	cmd.Dir = dir
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	if err := cmd.Run(); err != nil {
		return nil, fmt.Errorf("%s %s: %w%s",
			Binary, strings.Join(args, " "), err, diagnostic(stderr.String()))
	}
	return stdout.Bytes(), nil
}

// ansiPattern matches the colour escape sequences fnox writes to stderr.
var ansiPattern = regexp.MustCompile(`\x1b\[[0-9;]*[A-Za-z]`)

// diagnostic renders fnox's stderr as a suffix for an error message. fnox
// reports configuration and provider faults there, never secret values.
//
// The escapes are stripped rather than suppressed: --no-color does not reach
// the error renderer, and raw escape sequences inside a JSON protocol message
// reach the user as literal  noise.
func diagnostic(stderr string) string {
	trimmed := strings.TrimSpace(ansiPattern.ReplaceAllString(stderr, ""))
	if trimmed == "" {
		return ""
	}
	return ": " + strings.Join(strings.Fields(trimmed), " ")
}

// resolveBinary locates fnox before anything runs, so a host that never
// installed it fails with an instruction rather than with Go's bare
// "executable file not found in $PATH".
//
// A provider inherits the PATH of whatever ran docker compose, which for a
// systemd unit or another user is rarely the interactive shell's. The searched
// PATH is reported because that difference is the usual cause. A shell
// function wrapper, which is how mise exposes fnox interactively, is invisible
// here for the same reason.
func resolveBinary() (string, error) {
	path, err := exec.LookPath(Binary)
	if err != nil {
		return "", fmt.Errorf(
			"%s is not installed or not on the PATH of whatever runs docker compose: "+
				"install it from https://fnox.jdx.dev and put it on a system path "+
				"such as /usr/local/bin (searched PATH=%s)",
			Binary, os.Getenv("PATH"))
	}
	return path, nil
}
