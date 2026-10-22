package main

import (
	"fmt"

	"github.com/google/go-containerregistry/pkg/name"
	v1 "github.com/google/go-containerregistry/pkg/v1"
)

// cacheTagForPath derives the content-addressed cache tag for a built
// platform image: {ref}/cache/{image-name}:{nix-store-hash}. The store hash
// fully determines the tarball bytes, so an image already pushed under this
// tag is byte-identical to a fresh build and can be retagged instead of
// rebuilt and re-uploaded.
func cacheTagForPath(ref name.Reference, p *v1.Platform, path string) (*name.Tag, error) {
	image := imageName(ref)
	hash := nixStoreHash(path)
	if hash == "" {
		return nil, fmt.Errorf("no nix store hash in path %q", path)
	}
	tag, err := name.NewTag(fmt.Sprintf("%s/cache/%s:%s", ref.Context().Name(), image, hash))
	if err != nil {
		return nil, fmt.Errorf("failed to format cache reference: %w", err)
	}
	return &tag, nil
}

// imageName strips any tag from the reference and keeps the repository path's
// final segment (e.g. ghcr.io/shikanime-labs/machines/automata -> automata).
func imageName(ref name.Reference) string {
	repo := ref.Context().Name()
	for i := len(repo) - 1; i >= 0; i-- {
		if repo[i] == '/' {
			return repo[i+1:]
		}
	}
	return repo
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
