# Contributing

Thanks for helping make branches easier to see.

## Setup

You need Go (the version in `go.mod`), git 2.38 or newer, and Node 22 or newer for the web view.

```bash
go build ./cmd/vb
go test ./...
golangci-lint run ./...
```

Tests build real git repositories in temporary directories with isolated configuration, so they never touch your own git or vb settings.

## Trying changes by hand

```bash
go run ./tools/demo /tmp/vb-demo
cd /tmp/vb-demo/webapp
VB_GITHUB_FIXTURE=../prs.json go run ../../path/to/visual-branches/cmd/vb
```

The demo repository has stacks, agent worktrees that collide, squash merges, stale and unpushed work, and pull request data loaded from a fixture.
`VB_DEBUG=1` prints every git call with its duration.

## Web view

```bash
cd web
npm ci
npm run check   # strict TypeScript
npm run build   # writes internal/web/dist, which the binary embeds
```

Commit `internal/web/dist` with your change; CI fails when it is out of date.
While iterating, `npm run watch` and `VB_WEB_DIR=internal/web/dist vb web` serve the assets from disk.

## Guidelines

- vb is a map, not a manager: new features should read state, and anything that writes must be explicit and confirmed.
- Every surface renders the same `model.Map`; put logic in `internal/engine` and keep renderers thin.
- Keep output minimal and aligned; check changes at narrow widths and with `--ascii` and `--no-color`.
- The JSON schema is a public interface: add fields freely, but renaming or removing one needs a `schemaVersion` bump.
- Add a test with every behavior change, preferably an end-to-end one on a real repository (`internal/testrepo`).
- Render changes update golden files with `go test ./internal/render -update`; review the diff.
- Regenerate README images with `scripts/assets.sh` when the output changes visibly.

## Releases

Tag `vX.Y.Z` on `main`.
The release workflow builds binaries with GoReleaser and, when `TAP_GITHUB_TOKEN` is set, updates the Homebrew tap and Scoop bucket.
