// Package run holds the Compose lifecycle handlers.
package run

import (
	"context"
	"fmt"
	"maps"
	"regexp"
	"slices"
	"strings"

	compose "github.com/ktb-soft/go.compose-provider"

	"github.com/ktb-soft/docker-compose.fnox/internal/credentials"
	"github.com/ktb-soft/docker-compose.fnox/internal/fnox"
)

// envNamePattern matches the variable names a container runtime accepts.
var envNamePattern = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)

// Secrets is the part of fnox.Client this handler needs. Taking an interface
// rather than the concrete client is what lets the tests drive Up without a
// fnox binary anywhere in sight.
type Secrets interface {
	ConfigFiles(ctx context.Context, opts fnox.Options) ([]string, error)
	Sync(ctx context.Context, opts fnox.Options) error
	Export(ctx context.Context, opts fnox.Options) (map[string]string, error)
}

// checkContained fails the up when fnox would merge a config from a directory
// above the one named in the Compose file.
//
// path_config_toml reads as a boundary — these secrets, from this file — but
// fnox treats it as a starting point and merges every fnox.toml in the
// directories above it. An operator with secrets in their home directory would
// ship them to every container in the stack, and nothing in the Compose file
// would say so. Failing here is the conservative choice: over-injecting
// secrets is silent, while a failed up is not.
func checkContained(ctx context.Context, secrets Secrets, opts fnox.Options) error {
	loaded, err := secrets.ConfigFiles(ctx, opts)
	if err != nil {
		return err
	}

	parents := opts.ParentConfigs(loaded)
	if len(parents) == 0 {
		return nil
	}
	return fmt.Errorf(
		"%s would also load %s from the directories above it, and inject whatever they hold: "+
			"add `root = true` to %s to stop the search there",
		opts.ConfigPath, strings.Join(parents, ", "), opts.ConfigPath)
}

// Up resolves secrets through fnox and injects them into every service that
// declares depends_on against the provider service.
//
// The Compose contract requires up to be idempotent. Nothing is allocated, and
// the same inputs emit the same variables, so a repeated up is a repeated
// export.
func Up(secrets Secrets) compose.Handler {
	return func(ctx context.Context, req *compose.Request) error {
		opts, err := fnox.FromRequest(req)
		if err != nil {
			return err
		}

		if err := credentials.Apply(opts.CredentialsPath); err != nil {
			return err
		}

		if err := checkContained(ctx, secrets, opts); err != nil {
			return err
		}

		if opts.CacheEnabled {
			req.Emitter.Info("caching %s with %s", opts.Describe(), opts.CacheProvider)
			if err := secrets.Sync(ctx, opts); err != nil {
				return err
			}
		}

		req.Emitter.Info("resolving %s", opts.Describe())
		resolved, err := secrets.Export(ctx, opts)
		if err != nil {
			return err
		}

		names := slices.Sorted(maps.Keys(resolved))
		// Every name is checked before anything is emitted, so a bad key fails
		// the up instead of injecting a partial environment.
		for _, name := range names {
			if !envNamePattern.MatchString(name) {
				return fmt.Errorf("secret key %q in %s is not a valid environment variable name",
					name, opts.Describe())
			}
		}

		for _, name := range names {
			// RawSetEnv, not SetEnv: the variable has to arrive under its own
			// name. SetEnv would deliver DATABASE_URL as SECRETS_DATABASE_URL.
			req.Emitter.RawSetEnv(name, resolved[name])
		}

		if len(names) == 0 {
			req.Emitter.Info("0 secrets injected")
			return nil
		}
		// Names only. A secret value is written in exactly one place, the
		// RawSetEnv line above, and never in a message a user or a log sees.
		req.Emitter.Info("%d secrets injected: %s", len(names), strings.Join(names, ", "))
		return nil
	}
}
