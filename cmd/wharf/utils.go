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

func formatNixFlakePackageName(ref name.Reference) string {
	repo := ref.Context().RepositoryStr()
	segs := strings.Split(repo, "/")
	return segs[len(segs)-1]
}

func formatNixFlakePackage(buildContext string, ref name.Reference, p *v1.Platform) string {
	url, frag, hasFrag := strings.Cut(buildContext, "#")
	attr := ""
	if hasFrag {
		// An explicit #fragment names the package attribute directly so the
		// image name does not have to match the flake attribute. It is still
		// qualified per platform as packages.<system>.<attr>; the
		// packages.*. spelling is accepted for readability.
		attr = strings.TrimPrefix(frag, "packages.*.")
	}
	if attr == "" {
		attr = formatNixFlakePackageName(ref)
	}
	return fmt.Sprintf(
		"%s#packages.%s.%s",
		url,
		formatSystemName(p),
		attr,
	)
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
