# Nix Containers

Build OCI images from Nix flakes, with optional multi-platform output and push
to registries. Designed for use as a Skaffold custom builder, but usable
directly from the CLI.

**Language:** Go (Nix for build definitions)

## Structure

- `main.go` — root + `build`/`push` CLI command
- `cmd/wharf/` — all package code: CLI commands (`main.go`, `skaffold.go`),
  builder (`builder.go`), container runtime client (`container.go`), Nix build
  logic (`nix.go`, `nix_test.go`), and tracing (`tracing.go`)
- `flake.nix` — Dev shell and `packages.default` (broken via `buildGoModule`,
  offline `vendorHash = null` so deps fetch from the proxy rather than
  `vendor/`)

## Usage

Build container images directly from Nix flakes. Supports multi-platform builds
and pushing to container registries. Integrates with Skaffold as a custom
builder.

## Commit Style

- Plain-text capitalized title, no conventional-commit prefix
- Body with labels: `Design:`, `Related:`, `Closes #`
- Keep Markdown lines wrapped at 80 columns and run `nix fmt` before shipping

## Stack

- The commit title **is** the PR title; the commit body **is** the PR body
- Split work into stacked PRs to keep each PR small and reviewable
- To update a PR: edit files, then `jj squash` (or `git commit --amend`) into
  the **target commit** of the stack — the one that PR represents; the commit
  message updates the PR title and body automatically when resubmitted
- Never `gh pr merge` (creates poisoned commits)

## Protect `main`

- Require 1 approving review
- Require linear history (no merge commits)
- Require signed commits
- Squash+rebase merge only

_Licensed under AGPL-3.0. Test with both `docker` and `containerd` runtimes.
Always use worktrees when making changes._
