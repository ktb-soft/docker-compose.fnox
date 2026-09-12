// Command docker-compose-fnox is a Docker Compose provider that injects
// secrets resolved by fnox into services which depend on it.
//
// Compose invokes it as:
//
//	docker-compose-fnox compose --project-name <NAME> up \
//	    --path_config_toml=/srv/stack/fnox.toml "<service>"
package main

import (
	"os"

	compose "github.com/ktb-soft/go.compose-provider"

	"github.com/ktb-soft/docker-compose.fnox/internal/fnox"
	"github.com/ktb-soft/docker-compose.fnox/internal/run"
)

// version is the release tag, injected at build time with
// -ldflags="-X main.version=...".
var version = "dev"

func main() {
	os.Exit(compose.Main(compose.Provider{
		Name:        "docker-compose-fnox",
		Version:     version,
		Description: "Inject secrets resolved by fnox into dependent Compose services",
		Up:          run.Up(fnox.New()),
		UpParams:    fnox.Params,
		// Down and Stop stay nil. Nothing is allocated remotely, so there is
		// nothing to release and nothing to pause. Leaving Stop nil also keeps
		// the stop block out of the metadata output, which is how a provider
		// declines the docker compose stop hook.
	}))
}
