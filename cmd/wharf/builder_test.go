//go:generate go run github.com/matryer/moq@v0.7.1 -rm -stub -out builder_moq_test.go . nixBuilderClient:mockNixBuilderClient containerBuilderClient:mockContainerBuilderClient

package main

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/google/go-containerregistry/pkg/name"
	v1 "github.com/google/go-containerregistry/pkg/v1"
	"github.com/google/go-containerregistry/pkg/v1/mutate"
)

func mustParseReference(t *testing.T) name.Reference {
	t.Helper()

	ref, err := name.ParseReference("ghcr.io/example/app:latest")
	if err != nil {
		t.Fatalf("parse reference failed: %v", err)
	}
	return ref
}

func TestBuilderBuildAndPushReturnsPermissionErrorBeforeBuild(t *testing.T) {
	ref := mustParseReference(t)
	plat := &v1.Platform{OS: "linux", Architecture: "amd64"}
	nixClient := &mockNixBuilderClient{}
	containerClient := &mockContainerBuilderClient{
		CheckPushPermissionFunc: func(name.Reference) error {
			return errors.New("no credentials")
		},
	}

	builder := NewBuilder(nixClient, containerClient, WithPush(true))
	err := builder.BuildAndPush(context.Background(), "/workspace", ref, []*v1.Platform{plat})
	if err == nil || !strings.Contains(err.Error(), "no credentials") {
		t.Fatalf("expected permission error, got %v", err)
	}
	if len(nixClient.BuildPlatformImageCalls()) != 0 {
		t.Fatalf(
			"expected nix build to be skipped, got %d calls",
			len(nixClient.BuildPlatformImageCalls()),
		)
	}
	if len(containerClient.CheckPushPermissionCalls()) != 1 {
		t.Fatalf(
			"expected one permission check, got %d",
			len(containerClient.CheckPushPermissionCalls()),
		)
	}
}

func TestBuilderBuildAndPushSinglePlatformStreamFlow(t *testing.T) {
	ref := mustParseReference(t)
	plat := &v1.Platform{OS: "linux", Architecture: "amd64"}
	nixClient := &mockNixBuilderClient{
		BuildPlatformImageFunc: func(context.Context, string, name.Reference, *v1.Platform, ...imageOption) (string, error) {
			return "/tmp/result", nil
		},
	}
	containerClient := &mockContainerBuilderClient{
		PushImageFunc: func(name.Reference, string) error {
			return nil
		},
	}

	builder := NewBuilder(
		nixClient,
		containerClient,
		WithPush(true),
		WithStreamImageOption(WithAcceptFlakeConfig()),
	)
	if err := builder.BuildAndPush(
		context.Background(),
		"/workspace",
		ref,
		[]*v1.Platform{plat},
	); err != nil {
		t.Fatalf("build and push failed: %v", err)
	}

	buildCalls := nixClient.BuildPlatformImageCalls()
	if len(buildCalls) != 1 {
		t.Fatalf("expected one nix build, got %d", len(buildCalls))
	}
	if len(buildCalls[0].ImageOptionMoqParams) != 1 {
		t.Fatalf("expected image options to flow through builder")
	}
	pushImageCalls := containerClient.PushImageCalls()
	if len(pushImageCalls) != 1 || pushImageCalls[0].Reference.Name() != ref.Name() {
		t.Fatalf(
			"expected pushed ref %s, got calls=%d ref=%v",
			ref.Name(),
			len(pushImageCalls),
			pushImageCalls[0].Reference,
		)
	}
}

func TestBuilderBuildAndPushMultiplatformRequiresPush(t *testing.T) {
	ref := mustParseReference(t)
	plats := []*v1.Platform{
		{OS: "linux", Architecture: "amd64"},
		{OS: "linux", Architecture: "arm64"},
	}

	builder := NewBuilder(&mockNixBuilderClient{}, &mockContainerBuilderClient{}, WithPush(false))
	err := builder.BuildAndPush(context.Background(), "/workspace", ref, plats)
	if err == nil || !strings.Contains(err.Error(), "only supported when pushing") {
		t.Fatalf("expected multiplatform push error, got %v", err)
	}
}

func TestBuilderBuildAndPushRejectsEmptyPlatforms(t *testing.T) {
	ref := mustParseReference(t)
	containerClient := &mockContainerBuilderClient{}

	builder := NewBuilder(&mockNixBuilderClient{}, containerClient, WithPush(true))
	err := builder.BuildAndPush(context.Background(), "/workspace", ref, nil)
	if err == nil || !strings.Contains(err.Error(), "at least one platform is required") {
		t.Fatalf("expected empty platform error, got %v", err)
	}
	if len(containerClient.CheckPushPermissionCalls()) != 0 {
		t.Fatalf(
			"expected push permission check to be skipped, got %d",
			len(containerClient.CheckPushPermissionCalls()),
		)
	}
}

func TestBuilderBuildAndPushMultiplatformTracksImage(t *testing.T) {
	ref := mustParseReference(t)
	plats := []*v1.Platform{
		{OS: "linux", Architecture: "amd64"},
		{OS: "linux", Architecture: "arm64"},
	}
	nixClient := &mockNixBuilderClient{
		BuildPlatformImageFunc: func(context.Context, string, name.Reference, *v1.Platform, ...imageOption) (string, error) {
			return "/tmp/result", nil
		},
	}
	containerClient := &mockContainerBuilderClient{
		PushPlatformImageFunc: func(name.Reference, *v1.Platform, string) (mutate.IndexAddendum, error) {
			return mutate.IndexAddendum{}, nil
		},
	}

	builder := NewBuilder(nixClient, containerClient, WithPush(true))
	if err := builder.BuildAndPush(context.Background(), "/workspace", ref, plats); err != nil {
		t.Fatalf("multiplatform build and push failed: %v", err)
	}

	if len(nixClient.BuildPlatformImageCalls()) != 2 {
		t.Fatalf(
			"expected one nix build per platform, got %d",
			len(nixClient.BuildPlatformImageCalls()),
		)
	}
	if len(containerClient.PushPlatformImageCalls()) != 2 {
		t.Fatalf(
			"expected two platform pushes, got %d",
			len(containerClient.PushPlatformImageCalls()),
		)
	}
	if len(containerClient.PushManifestCalls()) != 1 {
		t.Fatalf("expected one manifest push, got %d", len(containerClient.PushManifestCalls()))
	}
}
