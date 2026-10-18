//go:generate go run github.com/matryer/moq@v0.7.1 -rm -stub -out builder_moq_test.go . nixBuilderClient:mockNixBuilderClient containerBuilderClient:mockContainerBuilderClient

package main

import (
	"context"
	"errors"
	"fmt"
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

func evalOutPathFunc(context.Context, string, name.Reference, *v1.Platform, ...imageOption) (string, error) {
	return "/nix/store/01234567890123456789012345678901-docker-image-app.tar.gz", nil
}

func cacheMissFunc(name.Reference, string) (v1.Image, error) {
	return nil, errors.New("cache miss")
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
		EvalOutPathFunc: evalOutPathFunc,
		BuildPlatformImageFunc: func(context.Context, string, name.Reference, *v1.Platform, ...imageOption) (string, error) {
			return "/tmp/result", nil
		},
	}
	containerClient := &mockContainerBuilderClient{
		CachedImageFunc: cacheMissFunc,
		PushImageFunc: func(name.Reference, string) error {
			return nil
		},
		AnnotateImageFunc: func(name.Reference, string) error {
			return nil
		},
	}

	builder := NewBuilder(
		nixClient,
		containerClient,
		WithPush(true),
		WithStreamImageOption(WithOption("accept-flake-config", "true")),
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
		EvalOutPathFunc: evalOutPathFunc,
		BatchBuildPlatformImagesFunc: func(_ context.Context, _ string, _ name.Reference, platforms []*v1.Platform, _ ...imageOption) ([]string, error) {
			paths := make([]string, len(platforms))
			for i := range platforms {
				paths[i] = fmt.Sprintf("/tmp/result-%d", i)
			}
			return paths, nil
		},
	}
	containerClient := &mockContainerBuilderClient{
		CachedImageFunc: cacheMissFunc,
		PushPlatformImageFunc: func(name.Reference, *v1.Platform, string) (mutate.IndexAddendum, error) {
			return mutate.IndexAddendum{}, nil
		},
		AnnotateImageFunc: func(name.Reference, string) error {
			return nil
		},
	}

	builder := NewBuilder(nixClient, containerClient, WithPush(true))
	if err := builder.BuildAndPush(context.Background(), "/workspace", ref, plats); err != nil {
		t.Fatalf("multiplatform build and push failed: %v", err)
	}

	batchCalls := nixClient.BatchBuildPlatformImagesCalls()
	if len(batchCalls) != 1 {
		t.Fatalf("expected one batched nix build, got %d", len(batchCalls))
	}
	if len(batchCalls[0].Platforms) != 2 {
		t.Fatalf("expected both platforms in the batch call, got %d", len(batchCalls[0].Platforms))
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

func TestBuilderBuildAndPushMultiplatformFailsWhenBatchBuildFails(t *testing.T) {
	ref := mustParseReference(t)
	plats := []*v1.Platform{
		{OS: "linux", Architecture: "amd64"},
		{OS: "linux", Architecture: "arm64"},
	}
	nixClient := &mockNixBuilderClient{
		EvalOutPathFunc: evalOutPathFunc,
		BatchBuildPlatformImagesFunc: func(context.Context, string, name.Reference, []*v1.Platform, ...imageOption) ([]string, error) {
			return nil, errors.New("nix-fast-build failed")
		},
	}
	containerClient := &mockContainerBuilderClient{
		CachedImageFunc: cacheMissFunc,
	}

	builder := NewBuilder(nixClient, containerClient, WithPush(true))
	err := builder.BuildAndPush(context.Background(), "/workspace", ref, plats)
	if err == nil || !strings.Contains(err.Error(), "nix-fast-build failed") {
		t.Fatalf("expected batch build error, got %v", err)
	}
	if len(containerClient.PushPlatformImageCalls()) != 0 {
		t.Fatalf(
			"expected no pushes after failed build, got %d",
			len(containerClient.PushPlatformImageCalls()),
		)
	}
}

func TestBuilderMultiplatformSkipsNixOnFullCacheHit(t *testing.T) {
	ref := mustParseReference(t)
	plats := []*v1.Platform{
		{OS: "linux", Architecture: "amd64"},
		{OS: "linux", Architecture: "arm64"},
	}
	nixClient := &mockNixBuilderClient{
		EvalOutPathFunc: func(_ context.Context, _ string, _ name.Reference, p *v1.Platform, _ ...imageOption) (string, error) {
			hash := "11111111111111111111111111111111"
			if p.Architecture == "arm64" {
				hash = "22222222222222222222222222222222"
			}
			return fmt.Sprintf("/nix/store/%s-docker-image-app.tar.gz", hash), nil
		},
	}
	containerClient := &mockContainerBuilderClient{
		CachedImageFunc: func(_ name.Reference, wantOutPath string) (v1.Image, error) {
			return nil, nil // annotation match by contract; mock returns the image
		},
	}

	builder := NewBuilder(nixClient, containerClient, WithPush(true))
	if err := builder.BuildAndPush(context.Background(), "/workspace", ref, plats); err != nil {
		t.Fatalf("cached multiplatform build failed: %v", err)
	}

	if calls := nixClient.BatchBuildPlatformImagesCalls(); len(calls) != 0 {
		t.Fatalf("expected nix build to be skipped on full cache hit, got %d calls", len(calls))
	}
	if calls := containerClient.PushPlatformImageCalls(); len(calls) != 0 {
		t.Fatalf("expected no platform pushes on full cache hit, got %d", len(calls))
	}
	if calls := containerClient.PushManifestCalls(); len(calls) != 1 {
		t.Fatalf("expected one manifest push from cache, got %d", len(calls))
	}
}

func TestBuilderMultiplatformBuildsOnlyCacheMissesAndRecordsTags(t *testing.T) {
	ref := mustParseReference(t)
	plats := []*v1.Platform{
		{OS: "linux", Architecture: "amd64"},
		{OS: "linux", Architecture: "arm64"},
	}
	nixClient := &mockNixBuilderClient{
		EvalOutPathFunc: func(_ context.Context, _ string, _ name.Reference, p *v1.Platform, _ ...imageOption) (string, error) {
			hash := "11111111111111111111111111111111"
			if p.Architecture == "arm64" {
				hash = "22222222222222222222222222222222"
			}
			return fmt.Sprintf("/nix/store/%s-docker-image-app.tar.gz", hash), nil
		},
		BatchBuildPlatformImagesFunc: func(_ context.Context, _ string, _ name.Reference, platforms []*v1.Platform, _ ...imageOption) ([]string, error) {
			paths := make([]string, len(platforms))
			for i := range platforms {
				paths[i] = "/nix/store/built-arm64-docker-image-app.tar.gz"
			}
			return paths, nil
		},
	}
	containerClient := &mockContainerBuilderClient{
		CachedImageFunc: func(_ name.Reference, wantOutPath string) (v1.Image, error) {
			if strings.Contains(wantOutPath, "11111111111111111111111111111111") {
				return nil, nil
			}
			return nil, errors.New("cache miss")
		},
		PushPlatformImageFunc: func(name.Reference, *v1.Platform, string) (mutate.IndexAddendum, error) {
			return mutate.IndexAddendum{}, nil
		},
		AnnotateImageFunc: func(name.Reference, string) error {
			return nil
		},
	}

	builder := NewBuilder(nixClient, containerClient, WithPush(true))
	if err := builder.BuildAndPush(context.Background(), "/workspace", ref, plats); err != nil {
		t.Fatalf("partial-miss multiplatform build failed: %v", err)
	}

	batchCalls := nixClient.BatchBuildPlatformImagesCalls()
	if len(batchCalls) != 1 || len(batchCalls[0].Platforms) != 1 {
		t.Fatalf("expected batch build of exactly one missed platform, got %d calls", len(batchCalls))
	}
	if batchCalls[0].Platforms[0].Architecture != "arm64" {
		t.Fatalf("expected arm64 to be the missed platform, got %s", batchCalls[0].Platforms[0].Architecture)
	}
	if calls := containerClient.PushPlatformImageCalls(); len(calls) != 1 {
		t.Fatalf("expected one platform push for the missed platform, got %d", len(calls))
	}
	annCalls := containerClient.AnnotateImageCalls()
	if len(annCalls) != 1 {
		t.Fatalf("expected one annotation recorded, got %d", len(annCalls))
	}
	if !strings.Contains(annCalls[0].S, "built-arm64") {
		t.Fatalf("expected annotation for the missed platform's built path, got %s", annCalls[0].S)
	}
	if calls := containerClient.PushManifestCalls(); len(calls) != 1 {
		t.Fatalf("expected one manifest push, got %d", len(calls))
	}
}
