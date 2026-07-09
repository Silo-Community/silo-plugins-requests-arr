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
