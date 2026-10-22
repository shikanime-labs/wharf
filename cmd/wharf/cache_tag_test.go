package main

import (
	"context"
	"errors"
	"testing"

	"github.com/google/go-containerregistry/pkg/name"
	v1 "github.com/google/go-containerregistry/pkg/v1"
)

func TestCacheTagForPath(t *testing.T) {
	ref, err := name.ParseReference("ghcr.io/shikanime-labs/machines/automata:v1.0.2", name.WeakValidation)
	if err != nil {
		t.Fatal(err)
	}
	tag, err := formatCacheTag(ref, "/nix/store/h14pps4qvfdf9rkpq4ch0hbzm19mybri-docker-image-automata.tar.gz")
	if err != nil {
		t.Fatal(err)
	}
	want := "ghcr.io/shikanime-labs/machines/automata/cache/automata:h14pps4qvfdf9rkpq4ch0hbzm19mybri"
	if tag.Name() != want {
		t.Fatalf("cache tag = %q, want %q", tag.Name(), want)
	}
}

func TestNixStoreHash(t *testing.T) {
	for _, tc := range []struct{ path, want string }{
		{"/nix/store/h14pps4qvfdf9rkpq4ch0hbzm19mybri-docker-image-automata.tar.gz", "h14pps4qvfdf9rkpq4ch0hbzm19mybri"},
		{"/nix/store/8rcjxggm57pjzyvrywjnxiwpal4342br-llama-cpp.tar.gz", "8rcjxggm57pjzyvrywjnxiwpal4342br"},
		{"no-store-prefix", ""},
	} {
		if got := nixStoreHash(tc.path); got != tc.want {
			t.Errorf("nixStoreHash(%q) = %q, want %q", tc.path, got, tc.want)
		}
	}
}

func TestBuildAndPushImageCacheHit(t *testing.T) {
	b := &Builder{
		nix: &mockNixBuilderClient{
			BuildPlatformImageFunc: func(context.Context, string, name.Reference, *v1.Platform, ...imageOption) (string, error) {
				return "/nix/store/h14pps4qvfdf9rkpq4ch0hbzm19mybri-docker-image-automata.tar.gz", nil
			},
		},
		container: &mockContainerBuilderClient{}, // CheckHeadFunc nil -> CheckHead returns nil -> hit
		push:      true,
	}
	ref, _ := name.ParseReference("ghcr.io/shikanime-labs/machines/automata:v1.0.2", name.WeakValidation)
	p := &v1.Platform{OS: "linux", Architecture: "arm64"}
	if err := b.buildAndPushImage(t.Context(), ".", ref, p); err != nil {
		t.Fatal(err)
	}
	calls := b.container.(*mockContainerBuilderClient)
	if n := len(calls.calls.PushImage); n != 0 {
		t.Fatalf("PushImage called %d times on cache hit, want 0", n)
	}
	if n := len(calls.calls.RetagImage); n != 1 {
		t.Fatalf("RetagImage called %d times on cache hit, want 1", n)
	}
}

func TestBuildAndPushImageCacheMiss(t *testing.T) {
	b := &Builder{
		nix: &mockNixBuilderClient{
			BuildPlatformImageFunc: func(context.Context, string, name.Reference, *v1.Platform, ...imageOption) (string, error) {
				return "/nix/store/h14pps4qvfdf9rkpq4ch0hbzm19mybri-docker-image-automata.tar.gz", nil
			},
		},
		container: &mockContainerBuilderClient{
			// HeadFunc errors = registry miss.
			CheckHeadFunc: func(name.Tag) error { return errors.New("MANIFEST_UNKNOWN") },
		},
		push: true,
	}
	ref, _ := name.ParseReference("ghcr.io/shikanime-labs/machines/automata:v1.0.2", name.WeakValidation)
	p := &v1.Platform{OS: "linux", Architecture: "arm64"}
	if err := b.buildAndPushImage(t.Context(), ".", ref, p); err != nil {
		t.Fatal(err)
	}
	calls := b.container.(*mockContainerBuilderClient)
	if n := len(calls.calls.PushImage); n != 1 {
		t.Fatalf("PushImage called %d times on miss, want 1", n)
	}
	if n := len(calls.calls.CheckHead); n != 1 {
		t.Fatalf("Head called %d times, want 1", n)
	}
	if n := len(calls.calls.RetagImage); n != 1 {
		t.Fatalf("RetagImage called %d times on miss (seed), want 1", n)
	}
}
