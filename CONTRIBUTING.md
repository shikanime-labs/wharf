# Contributing to wharf

Build OCI images from Nix flakes

## Workflow

Fork, branch off `main`, open a PR against `main`. One logical change per PR.

## Environment

```sh
direnv allow  # or: nix develop
```

## Validation

`go test ./...` green before submitting.

Security issues: see [SECURITY.md](SECURITY.md).
