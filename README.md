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

## Download progress

The plugin declares `reports_download_progress` and reports how far a
request's downloads are while they sit in the Sonarr or Radarr queue: a phase
(waiting, downloading, paused, stalled, importing, or import blocked), the
size and bytes left, and the latest estimated completion time. It reads the
same queue the plugin already checks for the request's status, so it needs no
settings.

- A season pack counts once, although Sonarr lists it under every episode.
- A season request counts only the downloads for its seasons.
- While any of a request's downloads has no known size yet, the size and bytes
  left are reported as 0, so Silo shows no percentage rather than an
  overstated one.
- While any of a request's downloads has failed, no progress is reported for
  it.

## Failed downloads

When Sonarr or Radarr reports one of a request's downloads as failed, the
plugin reads the service's Redownload Failed setting (Settings > Download
Clients > Failed Download Handling) to decide what Silo should do.

- With it on, the default, the service blocklists the release and searches
  for another, so the request stays in progress, with a message that the
  download failed and the service is looking for another release.
- With it off, nothing fetches the title again, so the plugin reports that
  server's part of the request as failed, with the reason. Silo marks the
  request failed, and an admin can retry it.
- When the setting cannot be read, the plugin treats it as on, so a
  transient error never fails a request.

The setting is read only while a request has a failed download, so other
status checks cost no extra call. A season request looks only at the
downloads for its seasons.

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
