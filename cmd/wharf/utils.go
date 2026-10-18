package main

import (
	"fmt"
	"strings"

	"github.com/google/go-containerregistry/pkg/name"
	v1 "github.com/google/go-containerregistry/pkg/v1"
)

func formatArch(s string) string {
	switch s {
	case "amd64":
		return "x86_64"
	case "arm64":
		return "aarch64"
	case "arm32":
		return "armv7l"
	default:
		return s
	}
}

func formatPlatformReference(ref name.Reference, p *v1.Platform) (*name.Tag, error) {
	tag, err := name.NewTag(fmt.Sprintf("%s_%s_%s", ref.Name(), p.OS, p.Architecture))
	if err != nil {
		return nil, fmt.Errorf("failed to format platform reference: %w", err)
	}
	return &tag, nil
}

func formatSystemName(p *v1.Platform) string {
	return fmt.Sprintf("%s-%s", formatArch(p.Architecture), p.OS)
}

// formatNixFlakePackageName resolves the package attribute: an explicit
// #fragment in the flake URL names it directly (skaffold pins like
// `.#automata-oci-image`); otherwise the image name's last segment. The
// `packages.*.` prefix is accepted for readability and stripped.
func formatNixFlakePackageName(ref name.Reference, flakeURL string) string {
	if _, frag, hasFrag := strings.Cut(flakeURL, "#"); hasFrag {
		frag = strings.TrimPrefix(frag, "packages.*.")
		if frag != "" {
			return frag
		}
	}
	repo := ref.Context().RepositoryStr()
	segs := strings.Split(repo, "/")
	return segs[len(segs)-1]
}

func formatNixFlakePackage(buildContext string, ref name.Reference, p *v1.Platform) string {
	url, _, _ := strings.Cut(buildContext, "#")
	return fmt.Sprintf(
		"%s#packages.%s.%s",
		url,
		formatSystemName(p),
		formatNixFlakePackageName(ref, buildContext),
	)
}

// formatNixFlakePackagesRoot returns the flake URL for batch evaluation
// with any #fragment stripped: nix-fast-build appends its own traversal and
// a stale fragment would corrupt the reference (`.#attr#packages`).
func formatNixFlakePackagesRoot(buildContext string) string {
	url, _, _ := strings.Cut(buildContext, "#")
	return url + "#packages"
}

// cacheTagName derives the registry cache tag for a nix store path: the
// 32-char store hash is a content identity over the whole closure (lock,
// sources, baked secrets), so nix-<hash> can only ever name one image.
// ponytail: cache tags accumulate as immutable orphans; add a sweeper if
// ghcr storage ever matters.
func cacheTagName(repo name.Repository, path string) (name.Tag, error) {
	base := path
	if i := strings.LastIndexByte(path, '/'); i >= 0 {
		base = path[i+1:]
	}
	if len(base) < 33 {
		return name.Tag{}, fmt.Errorf("store path %q carries no hash", path)
	}
	return name.NewTag(fmt.Sprintf("%s:nix-%s", repo.Name(), base[:32]))
}

// resolveFlakeURL returns the explicit flake URL when set, otherwise the
// build context path so nix commands keep targeting the local flake.
// Values starting with '-' are rejected so a flake URL can never be
// parsed as a nix flag.
func resolveFlakeURL(buildContext, flake string) (string, error) {
	flake = strings.TrimSpace(flake)
	if flake == "" {
		return buildContext, nil
	}
	if strings.HasPrefix(flake, "-") {
		return "", fmt.Errorf("invalid flake URL %q: must not start with '-'", flake)
	}
	return flake, nil
}
