# Silo Requests: Sonarr / Radarr

A Silo host plugin that fulfills Silo content requests (movies and series)
against one or more Sonarr/Radarr instances. It implements the
`request_router.v1` capability defined in the Silo plugin SDK, letting Silo
route an approved request to the correct instance (HD vs. 4K, anime overrides,
per-instance root folder / quality profile / tags) and trigger the add + search.

The plugin is stateless: it stores nothing. All credentials and per-connection
configuration (service endpoint, API key, root folder, quality profile, tags,
default/4K/anime flags, etc.) are supplied by the Silo host on every call.

## Season requests

The plugin declares `supports_seasons`, so Silo can send it a series request
that names seasons, including a request for the seasons a series in the
library is missing.

- A new series is added with only the requested seasons monitored, and the
  add-time search covers only them. Seasons Sonarr learns of later stay
  unmonitored.
- A series Sonarr already has keeps its other seasons as they are. The
  requested seasons, and every episode in them, are monitored and searched.
- A repeated request changes nothing more, and does not search a season whose
  aired episodes are all on disk.
- A request counts as complete when the aired, monitored episodes of its
  seasons are on disk; downloads for other seasons do not affect it.

The connection's Monitor policy applies only to whole-series requests. Radarr
ignores seasons.

## Build

```sh
make build
```

This produces a `plugin` binary. Use `make build-all` to cross-compile the
release matrix (linux/amd64, linux/arm64, darwin/arm64) into `dist/`.

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
