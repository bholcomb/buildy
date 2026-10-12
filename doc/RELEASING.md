# Releasing buildy

CI ([`.github/workflows/ci.yml`](../.github/workflows/ci.yml)) vets, tests and builds buildy on
Linux x64, Windows x64 and macOS on Apple Silicon for every push to `main` and every pull request.
A `v*` tag also publishes a GitHub release.

## Cutting a release

1. Set `Version` in [`src/main.go`](../src/main.go) to the new version (`0.3.0`), and commit that
   to `main`.
2. Wait for that commit's CI to pass.
3. Tag the commit and push the tag:

   ```bash
   git tag v0.3.0
   git push origin v0.3.0
   ```

The tag's CI builds each platform and checks that the binary reports the tag's version
(`buildy --version` prints `buildy 0.3.0 ...`). A tag whose commit says another version fails
there, and nothing is published: delete the tag, fix `Version`, and tag the new commit.

The source is the only place the version is written, so a binary built from source reports the
version of the source it was built from. A build of `main` after a release reports that release's
version until the next bump.

## What a release holds

| Asset | Platform |
|-------|----------|
| `buildy-<version>-linux-x64.tar.gz` | Linux x86-64 |
| `buildy-<version>-win-x64.zip` | Windows x64 |
| `buildy-<version>-osx-arm64.tar.gz` | macOS on Apple Silicon |
| `SHA256SUMS` | The SHA-256 of each archive |

Each archive holds just the binary, `buildy` (`buildy.exe` on Windows), built with `CGO_ENABLED=0`
and `-s -w`. The Linux binary is static, so it runs on any distribution. The macOS binary isn't
signed or notarized: a copy downloaded with a browser needs
`xattr -d com.apple.quarantine buildy` before it runs.

The release notes are GitHub's generated notes (the pull requests and commits since the last tag).
