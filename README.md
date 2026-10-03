# docker-compose-fnox

A [Docker Compose provider](https://docs.docker.com/compose/how-tos/provider-services/)
that resolves secrets with [fnox](https://fnox.jdx.dev) and injects them as
environment variables into the services that depend on it.

Secrets stay in fnox. The Compose file names a `fnox.toml`, and each
`docker compose up` resolves it fresh and hands the values to dependent
containers under their own names.

## Install

Requires `fnox` and a Docker Compose release with provider service support.

```sh
mise run build                                  # writes bin/docker-compose-fnox
sudo install bin/docker-compose-fnox /usr/local/bin/
```

Both `docker-compose-fnox` and `fnox` must be on the `PATH` of whatever runs
`docker compose`. For a systemd unit or another user, that is usually not your
interactive shell's `PATH`. A mise shell wrapper for `fnox` is not visible to
the provider, so install the real binary on a system path.

## Usage

Declare a provider service and make the services that need secrets depend on it:

```yaml
services:
  secrets:
    provider:
      type: docker-compose-fnox
      options:
        path_config_toml: /srv/stack/fnox.toml

  app:
    image: ghcr.io/example/app:latest
    depends_on:
      - secrets
```

With this `fnox.toml`:

```toml
root = true

[providers.plain]
type = "plain"

[secrets]
DATABASE_URL = { provider = "plain", value = "postgres://example" }
API_TOKEN = { provider = "plain", value = "not-a-real-token" }
```

`app` starts with `DATABASE_URL` and `API_TOKEN` in its environment. Names
are not prefixed with the provider service name.

`root = true` is required unless no `fnox.toml` exists in any parent
directory. See [Config containment](#config-containment).

### Options

| Option | Type | Default | Purpose |
| --- | --- | --- | --- |
| `path_config_toml` | string | required | `fnox.toml` to resolve secrets from |
| `path_credentials_env` | string | | Dotenv file of backend credentials for fnox |
| `profile` | string | | fnox profile, or comma separated profiles overlaid left to right |
| `all` | boolean | `false` | Include secrets marked `env = false` or `env = "exec"` |
| `if_missing` | `error`, `warn`, `ignore` | fnox's default | What fnox does when a secret cannot be resolved |
| `interactive` | boolean | `false` | Allow fnox prompts and browser auth flows |
| `cache_enabled` | boolean | `false` | Run `fnox sync` before exporting |
| `cache_provider` | string | | Provider that encrypts the cached copies |
| `cache_use_local_file` | boolean | `false` | Write the cache to `fnox.local.toml` instead of the config |

Run `docker-compose-fnox compose metadata` to print the same schema as JSON.

Rules that span options:

- `cache_provider` is required when `cache_enabled` is `true`.
- `cache_provider` and `cache_use_local_file` are rejected unless
  `cache_enabled` is `true`.
- `cache_use_local_file` requires the config to be named `fnox.toml` or
  `.fnox.toml`.

### Paths

Use an absolute `path_config_toml`. Compose does not define the working
directory a provider runs in, so a relative path resolves against wherever
`docker compose` was started.

fnox runs from the config's own directory. Relative paths inside the config,
such as an age provider's `key_file`, resolve against that directory.

### Backend credentials

Do not put tokens in `provider.options`. Options become process arguments and
appear in `docker compose config` output. Put them in a dotenv file and point
`path_credentials_env` at it:

```yaml
options:
  path_config_toml: /srv/stack/fnox.toml
  path_credentials_env: /srv/stack/fnox-credentials.env
```

```sh
# /srv/stack/fnox-credentials.env
OP_SERVICE_ACCOUNT_TOKEN=ops_...
```

The file accepts the dotenv subset `docker compose --env-file` accepts:
`KEY=value`, `#` comments, an optional `export` prefix, single quoted literal
values, and double quoted values with `\n`, `\r`, `\t`, `\"` and `\\` escapes.
A variable already set in the environment running `docker compose` wins over
the file. The file is read once, so a FIFO works.

### Config containment

fnox merges every `fnox.toml` it finds in the directories above the config.
A stack config under `/home/me/stacks/app/` would silently pick up
`/home/me/fnox.toml` and inject its secrets too. The provider asks fnox which
configs it will load and fails the `up` if any sit in a parent directory:

```
/srv/stack/fnox.toml would also load /srv/fnox.toml from the directories above it,
and inject whatever they hold: add `root = true` to /srv/stack/fnox.toml to stop the search there
```

Add `root = true` to the config. The global config in `~/.config/fnox` is
allowed.

### Offline cache

`cache_enabled: true` runs `fnox sync` before each export. Sync stores encrypted
copies of each secret so later runs resolve without reaching the upstream
backend. This writes to disk during `up`. Set `cache_use_local_file: true` to
write into `fnox.local.toml` beside the config, so a tracked `fnox.toml` does
not change on every `up`.

```yaml
options:
  path_config_toml: /srv/stack/fnox.toml
  cache_enabled: true
  cache_provider: age
  cache_use_local_file: true
```

### Lifecycle

Only `up` does work. `down` succeeds without doing anything because nothing is
allocated. `stop` is not declared, so Compose skips the provider on
`docker compose stop`.

### Failures

The `up` fails, and no variables are injected, when:

- `fnox` is not on the `PATH`
- fnox exits non-zero (missing config, unknown profile, a provider that cannot
  decrypt, or a missing secret under `if_missing: error`)
- a parent directory config would be merged
- a secret key is not a valid environment variable name

Progress messages name secrets but never print their values.

## Contributing

Tools are managed with [mise](https://mise.jdx.dev).

```sh
mise run test     # go test ./...
mise run check    # gofmt, go vet, go test
mise run build    # VERSION=v1.2.3 sets the reported version
```

How the provider works internally is in [docs/index.md](docs/index.md).

## Author

Brian Kenkel, [ktb-soft](https://fj.ktbcloud.com/ktb-soft).
