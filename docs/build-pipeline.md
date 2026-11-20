# Build Pipeline

How `wharf` turns a Nix flake into pushed OCI images. All code paths
live under `cmd/wharf/`.

## Flake inspection

`nix.go` shells out to `nix` through `exec.CommandContext`
(`nixCommandContext`) and classifies the flake's image package as:

- `StreamBuilderType` — streamable image package (`streamLayeredImage`
  output: run it and stream stdout), or
- `TarGzBuilderType` — tar.gz image package (`tarball.Image` reads the
  built path directly; no docker daemon involved).

The fixed nix options `accept-flake-config` and `no-pure-eval` are
pre-seeded; `--option key=value` pairs add more.

## Single-platform path

`buildAndPushImage` builds one platform via `BuildPlatformImage`, then
— when `PUSH_IMAGE` is set — runs the cache-tag probe below and either
retags or pushes, seeding the cache tag on a miss.

## Multi-platform fan-out

`buildMultiplatformImage` in `builder.go` builds every platform with
ONE batched invocation: `BatchBuildPlatformImages` in `nix.go` runs
`nix-fast-build --flake <root> --select <expr> --systems <list>
--stream-json-lines`. A single shared evaluation (one `nix-eval-jobs`
process) feeds parallel builds internally, so concurrent per-platform
nix processes can no longer race the shared eval-cache SQLite database
— the root cause of doubled `packages.<system>.` attribute segments and
"failed to parse nix build output: EOF" (issue #98). The flake package
wraps `nix-fast-build` into PATH via `makeWrapper`; a bare `go install`
build needs `nix-fast-build` and `nix-eval-jobs` in PATH.

The per-platform work that still runs concurrently through an `errgroup`
is the push/cache pipeline, not the builds: format the platform tag,
probe the cache tag, retag or push, then collect the index addenda.
After every platform lands, `PushManifest` writes the multi-arch OCI
index from the collected manifests.

## Registry cache tags

Every built platform image is content-addressed by its nix store path:
`formatCacheTag` in `utils.go` derives a cache tag of the form
`{repo}/cache/{image-name}:{nix-store-hash}` from the 32-character
base-32 store hash. Before uploading, the builder probes the cache tag
with a registry `HEAD` (`CheckHead` in `container.go`):

- **Cache hit** — the store hash fully determines the tarball bytes, so
  the cached image is byte-identical to a fresh build. `RetagImage` /
  `RetagPlatformImage` re-point the manifest onto the target tag with
  no layer upload.
- **Cache miss** — the image is pushed normally, and the same manifest
  is seeded onto the cache tag in the same pass (`PushPlatformImage`
  writes it as one extra PUT, no re-fetch; the single-platform path
  seeds via `RetagImage` after `PushImage`).

A failed `HEAD` probe is never fatal: it falls through to a normal push.

## Tracing

`tracing.go` sets up OpenTelemetry. Both command variants wrap their run
in `setupTracing(ctx)` and shut down on exit; the batched build and each
platform push run inside named spans.

## Skaffold mode

`skaffold.go` provides `wharf skaffold build`, the variant Skaffold
custom builders invoke. The pipeline is identical; `BUILD_CONTEXT` comes
from the environment.

## Configuration

`config.go` resolves `IMAGE`, `PLATFORMS`, `PUSH_IMAGE`, `LOG_LEVEL`,
`FLAKE`, and extra nix options from env vars and flags, backed by
viper. See the wiki CLI page for the full table.
