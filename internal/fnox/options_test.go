package fnox

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	compose "github.com/ktb-soft/go.compose-provider"
)

// request builds the Request Compose would hand a handler for the given
// options. Going through ParseRequest keeps the tests honest about the command
// line Compose actually produces.
func request(t *testing.T, options ...string) *compose.Request {
	t.Helper()

	args := append([]string{"compose", "--project-name", "stack", "up"}, options...)
	args = append(args, "secrets")

	req, err := compose.ParseRequest(args)
	if err != nil {
		t.Fatalf("ParseRequest(%v): %v", args, err)
	}
	return req
}

func TestFromRequest(t *testing.T) {
	req := request(t,
		"--path_config_toml=/srv/stack/fnox.toml",
		"--path_credentials_env=/run/secrets/fnox.env",
		"--cache_enabled=true",
		"--cache_provider=sync-age",
		"--cache_use_local_file=true",
		"--profile=prod",
		"--all=true",
		"--if_missing=warn",
	)

	opts, err := FromRequest(req)
	if err != nil {
		t.Fatalf("FromRequest: %v", err)
	}

	want := Options{
		ConfigPath:      "/srv/stack/fnox.toml",
		CredentialsPath: "/run/secrets/fnox.env",
		CacheEnabled:    true,
		CacheProvider:   "sync-age",
		CacheLocalFile:  true,
		Profile:         "prod",
		All:             true,
		IfMissing:       "warn",
	}
	if opts != want {
		t.Errorf("FromRequest = %+v, want %+v", opts, want)
	}
}

func TestFromRequestRejects(t *testing.T) {
	tests := []struct {
		name    string
		options []string
		want    string
	}{
		{
			name:    "config empty",
			options: []string{"--path_config_toml="},
			want:    "path_config_toml",
		},
		{
			name: "cache without provider",
			options: []string{
				"--path_config_toml=/srv/fnox.toml",
				"--cache_enabled=true",
			},
			want: "cache_provider is required",
		},
		{
			name: "cache settings without cache",
			options: []string{
				"--path_config_toml=/srv/fnox.toml",
				"--cache_provider=sync-age",
			},
			want: "no effect unless cache_enabled",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			_, err := FromRequest(request(t, test.options...))
			if err == nil {
				t.Fatalf("FromRequest(%v) succeeded, want error", test.options)
			}
			if !strings.Contains(err.Error(), test.want) {
				t.Errorf("error = %q, want it to mention %q", err, test.want)
			}
		})
	}
}

func TestExportArgs(t *testing.T) {
	tests := []struct {
		name string
		opts Options
		want []string
	}{
		{
			name: "minimal",
			opts: Options{ConfigPath: "/srv/fnox.toml"},
			want: []string{
				"--config", "fnox.toml", "--no-color", "--non-interactive",
				"export", "--format", "json",
			},
		},
		{
			name: "every flag",
			opts: Options{
				ConfigPath:  "/srv/fnox.toml",
				Profile:     "prod",
				IfMissing:   MissingWarn,
				All:         true,
				Interactive: true,
			},
			want: []string{
				"--config", "fnox.toml", "--no-color",
				"--profile", "prod", "--if-missing", "warn",
				"export", "--format", "json", "--all",
			},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := test.opts.exportArgs(); !slices.Equal(got, test.want) {
				t.Errorf("exportArgs() = %v, want %v", got, test.want)
			}
		})
	}
}

func TestSyncArgs(t *testing.T) {
	opts := Options{
		ConfigPath:     "/srv/fnox.toml",
		CacheEnabled:   true,
		CacheProvider:  "sync-age",
		CacheLocalFile: true,
	}

	want := []string{
		"--config", "fnox.toml", "--no-color", "--non-interactive",
		"sync", "--force", "--provider", "sync-age", "--local-file",
	}
	if got := opts.syncArgs(); !slices.Equal(got, want) {
		t.Errorf("syncArgs() = %v, want %v", got, want)
	}
}

// fnox runs with its working directory set to the config's directory, so a
// relative --config would be resolved against that directory a second time.
// Making the path absolute at parse time is what stops example/fnox.toml
// becoming example/example/fnox.toml.
func TestFromRequestMakesTheConfigPathAbsolute(t *testing.T) {
	opts, err := FromRequest(request(t, "--path_config_toml=example/fnox.toml"))
	if err != nil {
		t.Fatalf("FromRequest: %v", err)
	}

	if !filepath.IsAbs(opts.ConfigPath) {
		t.Fatalf("ConfigPath = %q, want an absolute path", opts.ConfigPath)
	}
	if filepath.Join(opts.dir(), "fnox.toml") != opts.ConfigPath {
		t.Errorf("dir() = %q is not the directory of %q", opts.dir(), opts.ConfigPath)
	}
}

// fnox resolves a relative key_file against its working directory, so the
// provider has to run it beside the config rather than wherever Compose left
// the process.
func TestDirIsTheConfigDirectory(t *testing.T) {
	opts := Options{ConfigPath: "/srv/stack/fnox.toml"}
	if got := opts.dir(); got != "/srv/stack" {
		t.Errorf("dir() = %q, want %q", got, "/srv/stack")
	}
}

// fnox only writes a local override beside a config it found by name, so an
// oddly named config and cache_use_local_file cannot both hold.
func TestFromRequestRejectsLocalFileWithAnOddConfigName(t *testing.T) {
	_, err := FromRequest(request(t,
		"--path_config_toml=/srv/stack.fnox.toml",
		"--cache_enabled=true",
		"--cache_provider=sync-age",
		"--cache_use_local_file=true",
	))
	if err == nil {
		t.Fatal("FromRequest succeeded, want the config name rejected")
	}
	if !strings.Contains(err.Error(), "fnox.toml or .fnox.toml") {
		t.Errorf("error = %q, want it to name the allowed filenames", err)
	}
}

func TestFromRequestDefaultsToAConfigInTheWorkingDirectory(t *testing.T) {
	for _, name := range []string{"fnox.toml", ".fnox.toml"} {
		t.Run(name, func(t *testing.T) {
			dir := t.TempDir()
			t.Chdir(dir)
			if err := os.WriteFile(name, nil, 0o600); err != nil {
				t.Fatal(err)
			}

			opts, err := FromRequest(request(t))
			if err != nil {
				t.Fatalf("FromRequest: %v", err)
			}
			if want := filepath.Join(dir, name); opts.ConfigPath != want {
				t.Errorf("ConfigPath = %q, want %q", opts.ConfigPath, want)
			}
		})
	}
}

func TestFromRequestPrefersFnoxTomlOverDotFnoxToml(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)
	for _, name := range []string{"fnox.toml", ".fnox.toml"} {
		if err := os.WriteFile(name, nil, 0o600); err != nil {
			t.Fatal(err)
		}
	}

	opts, err := FromRequest(request(t))
	if err != nil {
		t.Fatalf("FromRequest: %v", err)
	}
	if want := filepath.Join(dir, "fnox.toml"); opts.ConfigPath != want {
		t.Errorf("ConfigPath = %q, want %q", opts.ConfigPath, want)
	}
}

func TestFromRequestRejectsAMissingDefaultConfig(t *testing.T) {
	t.Chdir(t.TempDir())

	_, err := FromRequest(request(t))
	if err == nil {
		t.Fatal("FromRequest succeeded, want an error for the missing config")
	}
	if !strings.Contains(err.Error(), "no fnox.toml or .fnox.toml exists") {
		t.Errorf("error = %q, want it to name the default filenames", err)
	}
}
