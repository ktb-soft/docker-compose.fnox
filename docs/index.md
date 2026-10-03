# How docker-compose-fnox works

docker-compose-fnox is a single Go binary that Compose runs as a provider
service. It speaks the Compose provider protocol through
[`go.compose-provider`](https://github.com/ktb-soft/go.compose-provider) and
shells out to `fnox` for every secret operation.

## Invocation

Compose first runs `docker-compose-fnox compose metadata` to learn which options
each verb accepts. On `docker compose up` it runs:

```
docker-compose-fnox compose --project-name=<project> up \
    --path_config_toml=/srv/stack/fnox.toml [--<option>=<value> ...] <service>
```

The provider writes one JSON message per line on stdout. Compose renders `info`
and `error` messages and injects each `rawsetenv` variable into every service
that declares `depends_on` against the provider service.

## Verbs and options

`compose.Main` in [main.go](../main.go) dispatches the verb. Each verb has a
handler and a `Param` list:

| Verb | Handler | Params |
| --- | --- | --- |
| `up` | `run.Up` | `fnox.Params` |
| `down` | none | none |
| `stop` | none, not advertised | none |

Params are the schema. The library prints them as `metadata`, fills in declared
defaults, rejects undeclared options, and type checks booleans and enums before
the handler runs. Compose sends a verb only the options its metadata block
declares, so `down` receives no options and succeeds without doing anything.
Compose never calls `stop` because metadata has no `stop` block.

Options are the values in `provider.options`. `fnox.FromRequest` reads them into
the typed `fnox.Options` struct, applies the rules that span options, and makes
`path_config_toml` absolute.

## The up sequence

[`run.Up`](../internal/run/up.go) runs these steps in order. Any failure ends
the `up` before a variable is emitted.

1. **Read options.** `fnox.FromRequest` builds `fnox.Options` and validates it.
2. **Load credentials.** `credentials.Apply` reads `path_credentials_env` once
   and sets each variable in the provider process. Variables the host already
   set are kept. fnox inherits the result.
3. **Check containment.** `fnox config-files` lists every config fnox will
   merge. Any config in a parent directory of the target fails the `up`, with a
   message telling the operator to add `root = true`.
4. **Sync the cache.** Only when `cache_enabled` is true:
   `fnox sync --force --provider <cache_provider> [--local-file]`.
5. **Export.** `fnox export --format json [--all]`. Only the `secrets` object is
   kept. The `metadata` object is dropped because its timestamp would make
   identical runs differ.
6. **Validate names.** Every key must match `^[A-Za-z_][A-Za-z0-9_]*$`. All
   keys are checked before any are emitted.
7. **Emit.** `RawSetEnv` for each secret in sorted order, then one `info`
   message listing the names.

The handler is idempotent. A repeated `up` with the same inputs emits the same
variables.

## Running fnox

[`fnox.Client`](../internal/fnox/client.go) runs every command through a
`Runner` interface. `ExecRunner` runs the real binary. Tests replace it with a
fake.

Every command:

- runs with its working directory set to the config's directory
- passes `--config <basename>`, never the full path, because fnox rejects
  `--local-file` when `--config` holds a path
- passes `--no-color`
- passes `--non-interactive` unless `interactive` is true, because Compose gives
  the provider no terminal and a prompt would hang the `up`
- passes `--profile` and `--if-missing` when set

stdout and stderr are captured separately. On failure the error includes fnox's
stderr with colour escapes stripped and whitespace collapsed. fnox reports
configuration faults there, never secret values. When `fnox` is not on the
`PATH`, the error names the searched `PATH` and where to install it.

## Packages

| Package | Responsibility |
| --- | --- |
| `main` | Declares the provider: name, version, handlers, params |
| `internal/run` | The `up` handler and the containment check |
| `internal/fnox` | Option schema, option parsing, fnox command lines, process execution |
| `internal/credentials` | Dotenv parsing and loading into the process environment |

## Secret handling

A secret value appears in exactly one place: the `rawsetenv` line on stdout
that Compose reads. Progress messages and errors carry names and config paths
only. The library JSON encodes each message, so values containing quotes,
backslashes or newlines cannot break the stream.
