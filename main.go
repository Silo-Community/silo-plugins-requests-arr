// Command silo-plugins-requests-arr implements the Silo request_router.v1
// capability for multi-instance Sonarr/Radarr.
package main

import (
	_ "embed"

	"github.com/Silo-Server/silo-plugin-sdk/pkg/pluginsdk/runtime"

	"github.com/Silo-Community/silo-plugins-requests-arr/internal/router"
)

var version string

//go:embed manifest.json
var manifestJSON []byte

func main() {
	runtime.ServeManifest(manifestJSON, version, runtime.CapabilityServers{
		RequestRouter: router.New(),
	})
}
