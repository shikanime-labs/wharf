package main

import (
	"context"
	"fmt"
	"log/slog"
	"sync"

	"github.com/google/go-containerregistry/pkg/name"
	v1 "github.com/google/go-containerregistry/pkg/v1"
	"github.com/google/go-containerregistry/pkg/v1/mutate"
	"go.opentelemetry.io/otel/attribute"
	"golang.org/x/sync/errgroup"
)

type BuildOption func(*buildOption)

type buildOption struct {
	imageOpts []imageOption
	push      bool
}

type nixBuilderClient interface {
	BuildPlatformImage(
		context.Context,
		string,
		name.Reference,
		*v1.Platform,
		...imageOption,
	) (string, error)
	BatchBuildPlatformImages(
		context.Context,
		string,
		name.Reference,
		[]*v1.Platform,
		...imageOption,
	) ([]string, error)
	EvalOutPath(
		context.Context,
		string,
		name.Reference,
		*v1.Platform,
		...imageOption,
	) (string, error)
}

type containerBuilderClient interface {
	CheckPushPermission(name.Reference) error
	PushImage(name.Reference, string) error
	PushPlatformImage(name.Reference, *v1.Platform, string) (mutate.IndexAddendum, error)
	PushManifest(name.Reference, []mutate.IndexAddendum) error
	CachedImage(name.Reference, string) (v1.Image, error)
	AnnotateImage(name.Reference, string) error
	PushCachedImage(name.Reference, v1.Image) error
}

type Builder struct {
	nix       nixBuilderClient
	container containerBuilderClient
	imageOpts []imageOption
	push      bool
}

func NewBuilder(
	nix nixBuilderClient,
	container containerBuilderClient,
	opts ...BuildOption,
) *Builder {
	o := makeBuildOption(opts...)
	return &Builder{
		nix:       nix,
		container: container,
		imageOpts: o.imageOpts,
		push:      o.push,
	}
}

func WithStreamImageOption(opt imageOption) BuildOption {
	return func(o *buildOption) { o.imageOpts = append(o.imageOpts, opt) }
}

func WithPush(push bool) BuildOption {
	return func(o *buildOption) { o.push = push }
}

func makeBuildOption(opts ...BuildOption) *buildOption {
	o := &buildOption{}
	for _, opt := range opts {
		opt(o)
	}
	return o
}

func (b *Builder) BuildAndPush(
	ctx context.Context,
	buildContext string,
	ref name.Reference,
	plats []*v1.Platform,
) error {
	ctx, span := startSpan(ctx, "nix.build",
		attribute.String("ref", ref.Name()),
		attribute.Int("platform_count", len(plats)),
		attribute.Bool("push", b.push),
	)
	defer span.End()
	if len(plats) == 0 {
		return fmt.Errorf("at least one platform is required")
	}
	if b.push {
		slog.InfoContext(ctx, "checking push permission", "ref", ref.Name())
		// CheckPushPermission is used to fail fast if the user doesn't have credentials
		// to push to the registry. This prevents running the expensive build process
		// only to fail at the end.
		// See: https://github.com/google/go-containerregistry/issues/412
		if err := b.container.CheckPushPermission(ref); err != nil {
			return err
		}
	}
	if len(plats) == 1 {
		slog.DebugContext(ctx, "build image", "ref", ref.Name(), "plat", plats[0])
		return b.buildAndPushImage(ctx, buildContext, ref, plats[0])
	}
	slog.DebugContext(ctx, "build image", "ref", ref.Name(), "plats", plats)
	return b.buildAndPushMultiplatformImage(ctx, buildContext, ref, plats)
}

// buildPlatformPath runs the nix build for one platform and returns the
// local tarball path. The tarball is pushed directly (no docker load/tag) — the
// daemon round-trip was redundant and broke on docker 28.
func (b *Builder) buildPlatformPath(
	ctx context.Context,
	buildContext string,
	ref name.Reference,
	p *v1.Platform,
) (string, error) {
	bctx, bspan := startSpan(ctx, "nix.build.image",
		attribute.String("ref", ref.Name()),
		attribute.String("os", p.OS),
		attribute.String("arch", p.Architecture),
	)
	path, err := b.nix.BuildPlatformImage(
		bctx,
		buildContext,
		ref,
		p,
		b.imageOpts...,
	)
	bspan.End()
	if err != nil {
		return "", fmt.Errorf("build image failed: %w", err)
	}
	return path, nil
}

func (b *Builder) buildAndPushMultiplatformImage(
	ctx context.Context,
	buildContext string,
	ref name.Reference,
	ps []*v1.Platform,
) error {
	if !b.push {
		return fmt.Errorf(
			"multiplatform image build is only supported when pushing to remote registry",
		)
	}

	// Registry short-circuit: evaluate each platform's out path (cheap,
	// no build) and check whether the platform tag already holds that
	// exact image, matched by the nix store-path annotation on the
	// manifest. Every platform hit means the multi-arch manifest already
	// exists — skip nix entirely.
	type cacheEntry struct {
		img  v1.Image
		hit  bool
		path string
	}
	entries := make([]*cacheEntry, 0, len(ps))
	allHit := true
	for _, p := range ps {
		entry := &cacheEntry{}
		outPath, err := b.nix.EvalOutPath(ctx, buildContext, ref, p, b.imageOpts...)
		if err != nil {
			return fmt.Errorf("eval out path failed: %w", err)
		}
		entry.path = outPath
		platformTag, err := formatPlatformReference(ref, p)
		if err != nil {
			return fmt.Errorf("format platform reference failed: %w", err)
		}
		if img, err := b.container.CachedImage(platformTag, outPath); err == nil {
			entry.img = img
			entry.hit = true
			slog.InfoContext(
				ctx,
				"registry cache hit",
				"ref", ref.Name(),
				"platform", formatSystemName(p),
				"out_path", outPath,
			)
		} else {
			allHit = false
			slog.InfoContext(
				ctx,
				"registry cache miss",
				"ref", ref.Name(),
				"platform", formatSystemName(p),
				"out_path", outPath,
			)
		}
		entries = append(entries, entry)
	}
	if allHit {
		adds := make([]mutate.IndexAddendum, 0, len(entries))
		for i, entry := range entries {
			adds = append(adds, mutate.IndexAddendum{
				Add:        entry.img,
				Descriptor: v1.Descriptor{Platform: ps[i]},
			})
		}
		slog.InfoContext(ctx, "push manifest from cache", "ref", ref.Name(), "platform_count", len(adds))
		if err := b.container.PushManifest(ref, adds); err != nil {
			return err
		}
		slog.InfoContext(ctx, "manifest pushed from cache", "ref", ref.Name(), "platform_count", len(adds))
		return nil
	}

	var adds []mutate.IndexAddendum
	var addsMu sync.Mutex
	slog.InfoContext(ctx, "build multiplatform image", "ref", ref.Name(), "platform_count", len(ps))

	// One batched nix-fast-build invocation builds only the platforms
	// that missed the registry cache: a single evaluation feeds parallel
	// builds internally, so per-platform nix processes can no longer race
	// the shared eval-cache (issue #98).
	missedIdx := make([]int, 0, len(ps))
	missed := make([]*v1.Platform, 0, len(ps))
	for i, entry := range entries {
		if !entry.hit {
			missedIdx = append(missedIdx, i)
			missed = append(missed, ps[i])
		}
	}
	bctx, bspan := startSpan(ctx, "nix.build.batch",
		attribute.String("ref", ref.Name()),
		attribute.Int("platform_count", len(missed)),
	)
	missedPaths, err := b.nix.BatchBuildPlatformImages(bctx, buildContext, ref, missed, b.imageOpts...)
	bspan.End()
	if err != nil {
		return fmt.Errorf("build images failed: %w", err)
	}
	if len(missedPaths) != len(missed) {
		return fmt.Errorf(
			"build produced %d paths for %d platforms",
			len(missedPaths), len(missed),
		)
	}
	for j, i := range missedIdx {
		entries[i].path = missedPaths[j]
	}

	wg, ctx := errgroup.WithContext(ctx)
	for i, p := range ps {
		p, entry := p, entries[i]
		wg.Go(func() error {
			_, pspan := startSpan(ctx, "nix.build.platform",
				attribute.String("ref", ref.Name()),
				attribute.String("platform", formatSystemName(p)),
			)
			defer pspan.End()
			platformTag, err := formatPlatformReference(ref, p)
			if err != nil {
				return fmt.Errorf("format platform reference failed: %w", err)
			}
			var add mutate.IndexAddendum
			if entry.hit {
				// The platform tag already holds this digest; the index
				// still needs the addendum.
				slog.InfoContext(
					ctx,
					"platform image from cache",
					"ref", ref.Name(),
					"platform", formatSystemName(p),
				)
				add = mutate.IndexAddendum{
					Add:        entry.img,
					Descriptor: v1.Descriptor{Platform: p},
				}
			} else {
				slog.InfoContext(
					ctx,
					"push platform image",
					"ref", ref.Name(),
					"platform", formatSystemName(p),
					"path", entry.path,
				)
				add, err = b.container.PushPlatformImage(platformTag, p, entry.path)
			}
			if err != nil {
				return err
			}
			if !entry.hit {
				// Stamp the store-path annotation so the next run can
				// match this image without rebuilding it.
				if err := b.container.AnnotateImage(platformTag, entry.path); err != nil {
					return err
				}
			}
			addsMu.Lock()
			adds = append(adds, add)
			addsMu.Unlock()
			slog.InfoContext(
				ctx,
				"platform pipeline completed",
				"ref", ref.Name(),
				"platform", formatSystemName(p),
			)
			return nil
		})
	}
	if err := wg.Wait(); err != nil {
		return fmt.Errorf("push images failed: %w", err)
	}
	slog.InfoContext(ctx, "push manifest", "ref", ref.Name(), "platform_count", len(adds))
	if err := b.container.PushManifest(ref, adds); err != nil {
		return err
	}
	slog.InfoContext(ctx, "manifest pushed", "ref", ref.Name(), "platform_count", len(adds))
	return nil
}

func (b *Builder) buildAndPushImage(
	ctx context.Context,
	buildContext string,
	ref name.Reference,
	p *v1.Platform,
) error {
	if b.push {
		// Registry short-circuit: same closure hash means same image,
		// matched via the store-path annotation on the pushed manifest.
		outPath, err := b.nix.EvalOutPath(ctx, buildContext, ref, p, b.imageOpts...)
		if err != nil {
			return fmt.Errorf("eval out path failed: %w", err)
		}
		if img, err := b.container.CachedImage(ref, outPath); err == nil {
			slog.InfoContext(
				ctx,
				"registry cache hit",
				"ref", ref.Name(),
				"out_path", outPath,
			)
			return b.container.PushCachedImage(ref, img)
		}
		slog.InfoContext(
			ctx,
			"registry cache miss",
			"ref", ref.Name(),
			"out_path", outPath,
		)
		path, err := b.buildPlatformPath(ctx, buildContext, ref, p)
		if err != nil {
			return fmt.Errorf("build flake image failed: %w", err)
		}
		slog.DebugContext(ctx, "push image", "ref", ref.Name())
		if err := b.container.PushImage(ref, path); err != nil {
			return err
		}
		// Stamp the store-path annotation so the next run can match this
		// image without rebuilding it.
		return b.container.AnnotateImage(ref, outPath)
	}
	_, err := b.buildPlatformPath(ctx, buildContext, ref, p)
	if err != nil {
		return fmt.Errorf("build flake image failed: %w", err)
	}
	return nil
}
