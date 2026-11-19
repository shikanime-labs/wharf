<!-- owner: Automata | zone: technical | purpose: Canonical technical doc index for shikanime-labs/wharf -->
# wharf — technical index

A Go CLI that builds OCI images from Nix flakes, with optional multi-platform output and push to registries. Designed as a Skaffold custom builder but usable directly from the CLI.

## Canonical sources

- [Repository README](https://github.com/shikanime-labs/wharf#readme)
- [Technical wiki](https://github.com/shikanime-labs/wharf/wiki)

## Commands

- `wharf build [BUILD_CONTEXT]` — builds images from the flake at the context path (positional) and optionally pushes.
- `wharf skaffold build` — the Skaffold custom-builder entry point; reads `BUILD_CONTEXT` from the environment.

Configuration comes from flags or environment variables. Flags: `--flake` (explicit flake URL, overrides the context path), `--option key=value` (repeatable nix `--option` pair), `--platforms` (comma-separated `os/arch`). Key env vars: `IMAGE` (required target reference), `PLATFORMS` (defaults to host arch), `BUILD_CONTEXT`, `PUSH_IMAGE`, `LOG_LEVEL`, `FLAKE`.

## Subsystems

### Flake to image (`cmd/wharf/`)

`nix.go` shells out to `nix` via `exec.CommandContext`, inspects flake packages and classifies each as a streamable (`StreamBuilderType`) or tar.gz (`TarGzBuilderType`) image package. `accept-flake-config` and `no-pure-eval` are pre-seeded; `--option` adds more. Nix command output is parsed into image layers.

### Multi-platform fan-out

`builder.go` defines `nixBuilderClient` (per-platform `BuildPlatformImage` and batch `BatchBuildPlatformImages`) and `containerBuilderClient` (registry push and index assembly). Builds run concurrently through `errgroup`. With `PUSH_IMAGE`, each platform image is pushed first, then a multi-arch OCI index is assembled from the pushed manifests.

### Registry cache tags

`utils.go` `formatCacheTag` derives `{repo}/cache/{image-name}:{nix-store-hash}` from the 32-character base-32 store hash. `container.go` `CheckHead` probes the tag: on a hit, `RetagImage`/`RetagPlatformImage` re-point the manifest with no layer upload; on a miss the image is pushed normally and seeded onto the cache tag in the same pass. A failed probe falls through to a normal push. Pushing reads the nix-built tarball directly (`tarball.Image` + `remote.Write`) with no docker daemon; executable `streamLayeredImage` outputs are run and their stdout streamed.

### Tracing and configuration

`tracing.go` sets up OpenTelemetry; both command variants wrap their run in `setupTracing(ctx)` and shut down on exit. `config.go` resolves env and flag configuration via viper. `skaffold.go` hosts the `skaffold build` variant; the pipeline is otherwise identical.

## Documentation rule

In-repo `docs/` is canonical for technical detail. The [wiki](https://github.com/shikanime-labs/wharf/wiki) is a rendered human index that links here.
