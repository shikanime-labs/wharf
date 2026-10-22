package main

import (
	"fmt"
	"path/filepath"
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

// formatCacheTag derives the content-addressed cache tag for a built
// platform image: {repo}/cache/{image-name}:{nix-store-hash}. The store hash
// fully determines the tarball bytes, so an image already pushed under this
// tag is byte-identical to a fresh build and can be retagged instead of
// rebuilt and re-uploaded.
func formatCacheTag(ref name.Reference, p *v1.Platform, path string) (*name.Tag, error) {
	hash := nixStoreHash(path)
	if hash == "" {
		return nil, fmt.Errorf("no nix store hash in path %q", path)
	}
	image := filepath.Base(ref.Context().Name())
	tag, err := name.NewTag(fmt.Sprintf("%s/cache/%s:%s", ref.Context().Name(), image, hash))
	if err != nil {
		return nil, fmt.Errorf("failed to format cache reference: %w", err)
	}
	return &tag, nil
}

// nixStoreHash extracts the hash segment of a /nix/store/<hash>-<name> path.
// Nix store hashes are base-32, exactly 32 characters.
func nixStoreHash(path string) string {
	base := path
	for i := len(base) - 1; i >= 0; i-- {
		if base[i] == '/' {
			base = base[i+1:]
			break
		}
	}
	for i := 0; i < len(base); i++ {
		if base[i] != '-' {
			continue
		}
		hash := base[:i]
		if len(hash) == 32 {
			return hash
		}
		return ""
	}
	return ""
}
