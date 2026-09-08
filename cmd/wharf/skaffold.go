package main

import (
	"fmt"
	"log/slog"

	"github.com/spf13/cobra"
)

var (
	skaffoldCmd = &cobra.Command{
		Use:   "skaffold",
		Short: "Commands for Skaffold integration",
		Long:  "Subcommands intended to be invoked by Skaffold custom builders to build and push images using Nix flakes.",
		Example: "# Build via Skaffold custom builder\n" +
			"./wharf skaffold build --accept-flake-config",
	}

	skaffoldBuildCmd = &cobra.Command{
		Use:     "build",
		Short:   "Build and optionally push images",
		Long:    "Builds OCI images from a Nix flake and optionally pushes them to a registry. Configure via env vars: IMAGE, PLATFORMS, BUILD_CONTEXT, PUSH_IMAGE, LOG_LEVEL, ACCEPT_FLAKE_CONFIG, FLAKE.",
		Example: "IMAGE=ghcr.io/you/app:latest PLATFORMS=linux/amd64 PUSH_IMAGE=true BUILD_CONTEXT=. ACCEPT_FLAKE_CONFIG=true ./wharf skaffold build",
		RunE: func(cmd *cobra.Command, _ []string) error {
			ctx := cmd.Context()
			debug := getDebug()
			if debug {
				slog.SetLogLoggerLevel(slog.LevelDebug)
			}
			shutdown := setupTracing(ctx)
			defer func() {
				if err := shutdown(ctx); err != nil {
					slog.WarnContext(ctx, "tracing shutdown failed", "err", err)
				}
			}()
			buildContext := getBuildContext()
			flakeURL, err := resolveFlakeURL(buildContext, getFlakeURL())
			if err != nil {
				return err
			}
			ref, err := getImageTag()
			if err != nil {
				return fmt.Errorf("failed to get image: %w", err)
			}
			plats := getPlatforms()
			pushImage := getPushImage()
			extraOptions, err := getExtraOptions()
			if err != nil {
				return err
			}
			slog.InfoContext(
				ctx,
				"build config",
				"image", ref.String(),
				"platforms", plats,
				"build_context", buildContext,
				"flake_url", flakeURL,
				"push", pushImage,
				"debug", debug,
			)
			opts := []BuildOption{
				WithPush(pushImage),
			}
			for _, pair := range extraOptions {
				opts = append(opts, WithStreamImageOption(WithOption(pair[0], pair[1])))
			}
			container := NewContainerClient(ctx)
			builder := NewBuilder(NewNixClient(), container, opts...)
			return builder.BuildAndPush(ctx, flakeURL, ref, plats)
		},
	}
)

func init() {
	skaffoldCmd.AddCommand(skaffoldBuildCmd)
	rootCmd.AddCommand(skaffoldCmd)
}
