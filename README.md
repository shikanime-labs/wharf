# wharf

Build OCI images from Nix flakes, with optional multi-platform output and push
to registries. Designed for use as a Skaffold custom builder, but usable
directly from the CLI as well.

## Installation

- Go install: `go install github.com/shikanime-studio/wharf@latest`
- From source: `go build -o wharf .`

## Commands

- `wharf build [BUILD_CONTEXT]`
  - Builds images from the flake at `BUILD_CONTEXT` (positional, e.g., `.`) and
    optionally pushes.
- `wharf skaffold build`
  - Intended for Skaffold custom builders; reads `BUILD_CONTEXT` from env.

## Flags

- Global:
  - `--flake` Explicit flake URL to build (e.g., `github:org/repo`); overrides
    the build context path (also via `FLAKE`).
  - `--option key=value` Extra `--option key value` pair forwarded to the
    underlying nix command. Repeatable (also via `--option`). Use it for
    `accept-flake-config`, `pure-eval`, or any other nix setting.
  - `--platforms` Comma-separated platforms in `os/arch` form (e.g.,
    `linux/amd64,linux/arm64`). Overrides `PLATFORMS` env.

## Environment Variables

- `IMAGE` Required. Target image reference (e.g., `ghcr.io/you/app:latest`).
- `PLATFORMS` Optional. Comma-separated platforms (`linux/amd64,linux/arm64`).
  Defaults to host arch when unset. Overridden by `--platforms`.
- `BUILD_CONTEXT` Used by `skaffold build` (path to flake). For `build`, pass as
  positional argument.
- `PUSH_IMAGE` Optional boolean (`true|false|1|yes|on`). When true, images are
  pushed after build.
- `LOG_LEVEL` Optional (`info|debug|warn|error`). Defaults to `info`.
- `FLAKE` Optional. Explicit flake URL (e.g., `github:org/repo`). When set, all
  nix commands target that flake instead of the build context path. Can also be
  set via `--flake`.

## Examples

### Direct CLI

- Single platform build (no push):
  - `IMAGE=ghcr.io/you/app:latest ./wharf build .`

- Multi-platform build and push via env:

  ```text
  IMAGE=ghcr.io/you/app:latest PLATFORMS=linux/amd64,linux/arm64 \
    PUSH_IMAGE=true ./wharf build .
  ```

- Multi-platform build via flags, forwarding a nix option:

  ```text
  IMAGE=ghcr.io/you/app:latest PUSH_IMAGE=true \
    ./wharf build --platforms linux/amd64,linux/arm64 \
    --option accept-flake-config=true .
  ```

### Skaffold Usage

```yaml
apiVersion: skaffold/v4beta11
kind: Config
metadata:
  name: wharf
build:
  artifacts:
    - image: ghcr.io/you/app:latest
      custom:
        buildCommand: ./wharf skaffold build --option accept-flake-config=true
deploy:
  kubectl:
    manifests:
      - k8s/*.yaml
```

At build time, set the following env vars for the custom builder:

- `IMAGE=ghcr.io/you/app:latest`
- `BUILD_CONTEXT=.`
- `PLATFORMS=linux/amd64,linux/arm64` (or use `--platforms` in `buildCommand`)
- `PUSH_IMAGE=true`

## Notes

- Authentication uses Docker credential helpers via the default keychain.
- When building multi-platform images with push enabled, individual platform
  images are pushed first, then a multi-arch index is written.
