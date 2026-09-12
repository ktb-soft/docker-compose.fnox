// Package fnox turns Compose provider options into fnox invocations and runs
// them.
package fnox

import (
	"fmt"
	"path/filepath"
	"strings"

	compose "github.com/ktb-soft/go.compose-provider"
)

// Missing policies fnox accepts for its --if-missing flag.
const (
	MissingError  = "error"
	MissingWarn   = "warn"
	MissingIgnore = "ignore"
)

// Option names as they appear in the provider.options block of a Compose file.
// They are also the process flags Compose passes, so every name here shows up
// twice: once in compose.yaml and once on the fnox-compose command line.
const (
	OptConfigPath      = "path_config_toml"
	OptCredentialsPath = "path_credentials_env"
	OptCacheEnabled    = "cache_enabled"
	OptCacheProvider   = "cache_provider"
	OptCacheLocalFile  = "cache_use_local_file"
	OptProfile         = "profile"
	OptAll             = "all"
	OptIfMissing       = "if_missing"
	OptInteractive     = "interactive"
)

// Params declares the options to the Compose extension protocol. The provider
// package renders these as the `metadata` subcommand output, fills in declared
// defaults before the handler runs, rejects options that are not listed, and
// type checks the booleans.
var Params = []compose.Param{
	{
		Name:        OptConfigPath,
		Description: "Path to the fnox.toml to resolve secrets from",
		Required:    true,
	},
	{
		Name:        OptCredentialsPath,
		Description: "Path to a dotenv file holding backend credentials for fnox",
	},
	{
		Name:        OptCacheEnabled,
		Description: "Run `fnox sync` before exporting, so secrets resolve without reaching the upstream backend",
		Type:        compose.ParamBoolean,
		Default:     "false",
	},
	{
		Name:        OptCacheProvider,
		Description: "Provider `fnox sync` encrypts the cached copies with, required when " + OptCacheEnabled,
	},
	{
		Name:        OptCacheLocalFile,
		Description: "Write the cache to fnox.local.toml instead of into the config file itself",
		Type:        compose.ParamBoolean,
		Default:     "false",
	},
	{
		Name:        OptProfile,
		Description: "fnox profile, or comma separated profiles overlaid left to right",
	},
	{
		Name:        OptAll,
		Description: `Include secrets marked env = false or env = "exec"`,
		Type:        compose.ParamBoolean,
		Default:     "false",
	},
	{
		Name:        OptIfMissing,
		Description: "What fnox does when a secret cannot be resolved",
		Enum:        []string{MissingError, MissingWarn, MissingIgnore},
	},
	{
		Name:        OptInteractive,
		Description: "Allow fnox to prompt and open browser auth flows",
		Type:        compose.ParamBoolean,
		Default:     "false",
	},
}

// Options is the parsed provider.options block of one Compose service.
type Options struct {
	// ConfigPath is the fnox.toml to resolve against. It is required: Compose
	// does not define the working directory a provider runs in, so fnox's own
	// search up the directory tree has no reliable place to start.
	ConfigPath string
	// CredentialsPath is a dotenv file of backend credentials, loaded into the
	// provider process before fnox runs.
	CredentialsPath string
	// CacheEnabled runs `fnox sync` before the export.
	CacheEnabled bool
	// CacheProvider encrypts the cached copies, typically an age provider.
	CacheProvider string
	// CacheLocalFile sends the cache to fnox.local.toml rather than into the
	// config file, which keeps a tracked fnox.toml from churning on every up.
	CacheLocalFile bool
	// Profile selects a fnox profile. fnox handles comma separated values.
	Profile string
	// All includes secrets fnox excludes from an export by default.
	All bool
	// IfMissing overrides what fnox does with an unresolvable secret. Empty
	// leaves fnox's own default in place.
	IfMissing string
	// Interactive allows prompts and browser auth. Off by default: Compose
	// gives a provider no terminal, so a prompt hangs the up instead of
	// failing it.
	Interactive bool
}

// FromRequest reads the options Compose passed. The provider package has
// already rejected unknown names, applied the defaults declared in Params and
// checked that the booleans parse, so the only work left is reading values and
// the cross-field rules no single Param can express.
func FromRequest(req *compose.Request) (Options, error) {
	var (
		opts Options
		err  error
	)

	read := func(f func() error) {
		if err == nil {
			err = f()
		}
	}
	str := func(name string, target *string) func() error {
		return func() error {
			v, e := req.Options.StringDefault(name, "")
			*target = v
			return e
		}
	}
	boolean := func(name string, target *bool) func() error {
		return func() error {
			v, e := req.Options.BoolDefault(name, false)
			*target = v
			return e
		}
	}

	read(str(OptConfigPath, &opts.ConfigPath))
	read(str(OptCredentialsPath, &opts.CredentialsPath))
	read(str(OptCacheProvider, &opts.CacheProvider))
	read(str(OptProfile, &opts.Profile))
	read(str(OptIfMissing, &opts.IfMissing))
	read(boolean(OptCacheEnabled, &opts.CacheEnabled))
	read(boolean(OptCacheLocalFile, &opts.CacheLocalFile))
	read(boolean(OptAll, &opts.All))
	read(boolean(OptInteractive, &opts.Interactive))
	if err != nil {
		return Options{}, err
	}

	if err := opts.validate(); err != nil {
		return Options{}, err
	}

	// The config path is made absolute here, once. fnox runs with its working
	// directory set to the config's own directory, so a relative path would
	// otherwise be resolved against that directory a second time and turn
	// example/fnox.toml into example/example/fnox.toml.
	absolute, err := filepath.Abs(opts.ConfigPath)
	if err != nil {
		return Options{}, fmt.Errorf("resolving %s: %w", OptConfigPath, err)
	}
	opts.ConfigPath = absolute

	return opts, nil
}

// validate covers the rules that span more than one option, which Params
// cannot express on its own.
func (o Options) validate() error {
	if strings.TrimSpace(o.ConfigPath) == "" {
		return fmt.Errorf("option %s is empty", OptConfigPath)
	}
	if o.CacheEnabled && o.CacheProvider == "" {
		return fmt.Errorf("option %s is required when %s is true",
			OptCacheProvider, OptCacheEnabled)
	}
	if !o.CacheEnabled && (o.CacheProvider != "" || o.CacheLocalFile) {
		return fmt.Errorf("%s and %s have no effect unless %s is true",
			OptCacheProvider, OptCacheLocalFile, OptCacheEnabled)
	}
	// fnox finds the local override beside a config it located by name, so it
	// only writes one for a config called fnox.toml or .fnox.toml. Catching it
	// here fails the up before anything has run, rather than midway with a
	// message about a flag the Compose file never mentions.
	if o.CacheLocalFile {
		if base := filepath.Base(o.ConfigPath); base != "fnox.toml" && base != ".fnox.toml" {
			return fmt.Errorf(
				"%s requires the config to be named fnox.toml or .fnox.toml, got %q",
				OptCacheLocalFile, base)
		}
	}
	return nil
}

// dir is the working directory fnox runs in. Relative paths inside a config —
// an age provider's key_file, most commonly — resolve against it, so it has to
// be the directory holding the config rather than whatever Compose happened to
// leave the process in.
func (o Options) dir() string {
	return filepath.Dir(o.ConfigPath)
}

// globalArgs renders the flags fnox accepts ahead of any subcommand. Both the
// export and the sync command line start with these.
//
// --config gets the bare filename, never the full path, with dir() carrying
// the location. fnox rejects --local-file when --config holds an explicit
// path, because a path names one file and the local override is found by
// looking beside the working directory's config.
func (o Options) globalArgs() []string {
	args := []string{"--config", filepath.Base(o.ConfigPath), "--no-color"}
	if !o.Interactive {
		args = append(args, "--non-interactive")
	}
	if o.Profile != "" {
		args = append(args, "--profile", o.Profile)
	}
	if o.IfMissing != "" {
		args = append(args, "--if-missing", o.IfMissing)
	}
	return args
}

// exportArgs is the command line that resolves secrets.
func (o Options) exportArgs() []string {
	args := append(o.globalArgs(), "export", "--format", "json")
	if o.All {
		args = append(args, "--all")
	}
	return args
}

// syncArgs is the command line that refreshes the offline cache.
//
// --force skips the confirmation prompt. sync rewrites a config file, so it
// asks first when a human runs it; Compose gives a provider no terminal, and
// an unanswered prompt would hang the up rather than fail it.
func (o Options) syncArgs() []string {
	args := append(o.globalArgs(), "sync", "--force", "--provider", o.CacheProvider)
	if o.CacheLocalFile {
		args = append(args, "--local-file")
	}
	return args
}

// ParentConfigs returns the loaded configs that sit in a directory above the
// one holding the target config.
//
// fnox walks up from its working directory and merges every fnox.toml it
// passes, so an operator's ~/fnox.toml is layered under a project config
// without either file mentioning the other. Those are the files that turn a
// narrow config into a wide one.
//
// The user's global config is deliberately not flagged. It lives in
// ~/.config/fnox rather than in an ancestor of the project, so it is never a
// surprise inherited from where the stack happens to sit on disk, and it is
// where shared providers are normally declared.
func (o Options) ParentConfigs(loaded []string) []string {
	dir := o.dir()

	var parents []string
	for _, file := range loaded {
		if file == o.ConfigPath {
			continue
		}
		if isAncestor(filepath.Dir(file), dir) {
			parents = append(parents, file)
		}
	}
	return parents
}

// isAncestor reports whether dir is a strict parent of child.
func isAncestor(dir, child string) bool {
	relative, err := filepath.Rel(dir, child)
	if err != nil {
		return false
	}
	return relative != "." && !strings.HasPrefix(relative, "..")
}

// configFilesArgs is the command line that reports which configs fnox loads.
func (o Options) configFilesArgs() []string {
	return append(o.globalArgs(), "config-files")
}

// Describe names the config an operation ran against, for progress messages.
func (o Options) Describe() string {
	if o.Profile == "" {
		return o.ConfigPath
	}
	return o.ConfigPath + " (profile " + o.Profile + ")"
}
