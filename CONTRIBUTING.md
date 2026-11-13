# Contributing to the Sonarr & Radarr Requests Plugin

The [Silo contribution guide](https://github.com/Silo-Server/.github/blob/main/CONTRIBUTING.md)
covers project-wide coordination, focused changes, evidence, AI disclosure, and
pull request expectations. Those requirements apply here; this guide adds the
plugin-specific workflow.

## Release

Maintainers cut releases. Pushing a `v<semver>` tag runs
[`release.yml`](.github/workflows/release.yml), which calls the shared
`Silo-Server/.github` plugin release workflow. It builds the binaries, signs
them with GitHub artifact attestations, publishes the GitHub release, and asks
the [approved community catalog](https://github.com/Silo-Community/silo-plugins) to
pick it up. There is no manual-dispatch release.

The catalog is notified only for a full release, and only when this repository
has the `SILO_PLUGINS_DISPATCH_TOKEN` secret. A prerelease tag (`vX.Y.Z-rc.1`)
publishes a GitHub prerelease and never notifies the catalog. If a full
release's notification failed, or was skipped because the secret was missing,
set the secret and re-run the release run's `Notify the silo-plugins catalog`
job.

1. Open a pull request that sets `version` in `manifest.json` to the new
   version (for example `0.1.12`), and merge it.
2. Tag the commit that pull request put on `main` and push the tag. Tag that
   commit itself, not a later one:

   ```sh
   git switch main && git pull --ff-only
   git tag v0.1.12 <commit that changed manifest.json>
   git push origin v0.1.12
   ```

The workflow fails without publishing anything if the tag is not `v<semver>`,
`manifest.json` `version` does not match the tag, or the tagged commit is not
the commit on `main` that changed `version`. Only that one commit can be
released for a version. Do not move or delete a pushed tag; fix `manifest.json`
in a new pull request and tag the next version.

Each release carries `plugin-<os>-<arch>` binaries, `checksums.txt`, and
`plugin.sigstore.json`. To verify a download, check its attestation with the
GitHub CLI, naming the release's tag:

```sh
gh attestation verify plugin-linux-amd64 \
  --repo Silo-Community/silo-plugins-requests-arr \
  --cert-identity https://github.com/Silo-Server/.github/.github/workflows/plugin-release.yml@refs/heads/main \
  --source-ref refs/tags/vX.Y.Z \
  --deny-self-hosted-runners
```

It confirms that the shared release workflow on its `main` branch built the
binary from that tag of this repository. To check
against the bundle shipped with the release instead of fetching the attestation
from GitHub, add `--bundle plugin.sigstore.json`; `gh` still fetches Sigstore's
trusted root unless you follow GitHub's
[offline verification steps](https://docs.github.com/en/actions/how-tos/secure-your-work/use-artifact-attestations/verify-attestations-offline).
Verify before running a downloaded binary.
`sha256sum plugin-linux-amd64` (on macOS, `shasum -a 256 plugin-darwin-arm64`)
must also match the binary's line in `checksums.txt`.
