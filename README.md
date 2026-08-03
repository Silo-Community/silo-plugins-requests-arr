# Silo Requests: Sonarr / Radarr

A Silo host plugin that fulfills Silo content requests (movies and series)
against one or more Sonarr/Radarr instances. It implements the
`request_router.v1` capability defined in the Silo plugin SDK, letting Silo
route an approved request to the correct instance (HD vs. 4K, anime overrides,
per-instance root folder / quality profile / tags) and trigger the add + search.

The plugin is stateless: it stores nothing. All credentials and per-connection
configuration (service endpoint, API key, root folder, quality profile, tags,
default/4K/anime flags, etc.) are supplied by the Silo host on every call.

## Build

```sh
CGO_ENABLED=0 GOWORK=off make build
```

This produces a `plugin` binary. Use `make build-all` to cross-compile the
release matrix (linux/amd64, linux/arm64, darwin/arm64) into `dist/`.

## Routing and anime roles

Configure Sonarr connections for series and Radarr connections for movies. For
each service kind independently, assign at most one standard default and one
anime default per tier:

- `is_default` / `is_anime_default` select the HD (1080p) tier.
- `is_default_4k` / `is_anime_default_4k` select the 4K (2160p) tier; the
  connection must also be marked `is_4k`.

For an anime request, the plugin selects the matching service kind and tier in
this exact order: `is_anime_default` or `is_anime_default_4k`, then the normal
`is_default` or `is_default_4k`. A non-anime request uses only the normal
default. Thus Sonarr anime series never select Radarr roles, and Radarr anime
movies never select Sonarr roles; HD and 4K decisions are independent.

An anime-role target always uses anime fulfillment behavior. When routing falls
back to a normal default, the legacy `anime_enabled` flag still enables anime
behavior on that connection. Anime root folder, quality profile, and tags are
overrides: leave any of them blank to inherit that connection's normal value.

## Local installation

For a local, non-production smoke test, build as above, then open **Silo
Admin → Plugins → Catalog → Manual Install**, upload the `plugin` binary, and
configure the Sonarr/Radarr connection roles. Back up the target host before
testing and submit a harmless non-production request to confirm the expected
instance and tier route.

Re-uploading a plugin with the same plugin ID replaces its binary and manifest
while preserving the existing installation and configuration. Treat that as an
in-place update: verify the backup and smoke test rather than uploading to a
live user host.

## Community maintenance

This is an approved community plugin maintained in the
[`Silo-Community`](https://github.com/Silo-Community) organization. Use
[GitHub Issues](https://github.com/Silo-Community/silo-plugins-requests-arr/issues)
for support and bug reports. Security reports should follow
[`SECURITY.md`](SECURITY.md).

The plugin consumes the published
[`silo-plugin-sdk`](https://github.com/Silo-Server/silo-plugin-sdk); CI rejects
machine-local SDK replacement directives.

## License

Licensed under AGPL-3.0. See [`LICENSE`](LICENSE).
