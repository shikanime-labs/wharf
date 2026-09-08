package main

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os/exec"
	"strings"
	"sync"

	"github.com/google/go-containerregistry/pkg/name"
	v1 "github.com/google/go-containerregistry/pkg/v1"
	"golang.org/x/sync/errgroup"
)

var nixCommandContext = exec.CommandContext

// BuilderType indicates the type of a Nix flake package.
type BuilderType int

const (
	// UnknownBuilderType indicates the package type is unknown.
	UnknownBuilderType BuilderType = iota
	// StreamBuilderType indicates a streamable image package.
	StreamBuilderType
	// TarGzBuilderType indicates a tar.gz package.
	TarGzBuilderType
)

type imageOption func(*imageOptions)

type imageOptions struct {
	acceptFlakeConfig bool
	noPureEval        bool
}

type NixClient struct{}

type flakeShowPackage struct {
	Name string `json:"name"`
	Type string `json:"type"`
}

type flakeShowOutput struct {
	Packages map[string]map[string]flakeShowPackage `json:"packages"`
}

type buildImageBuildResult struct {
	DrvPath   string            `json:"drvPath"`
	Outputs   map[string]string `json:"outputs"`
	StartTime int64             `json:"startTime"`
	StopTime  int64             `json:"stopTime"`
}

func formatNixBuildError(err error, stderr string) error {
	stderr = strings.TrimSpace(stderr)
	if stderr == "" {
		return err
	}
	return fmt.Errorf("%w: %s", err, stderr)
}

func handleNixBuildError(
	ctx context.Context,
	url string,
	err error,
	stderrOutput *strings.Builder,
	stderrMu *sync.Mutex,
) error {
	stderrMu.Lock()
	stderr := stderrOutput.String()
	stderrMu.Unlock()

	err = formatNixBuildError(err, stderr)
	slog.ErrorContext(ctx, "nix build failed", "url", url, "err", err)
	return err
}

func handleNixBuild(
	sc *bufio.Scanner,
	stderrOutput *strings.Builder,
	stderrMu *sync.Mutex,
) error {
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" {
			continue
		}

		stderrMu.Lock()
		if stderrOutput.Len() > 0 {
			stderrOutput.WriteString("\n")
		}
		stderrOutput.WriteString(line)
		stderrMu.Unlock()
	}
	if err := sc.Err(); err != nil {
		return fmt.Errorf("stderr scan failed: %w", err)
	}
	return nil
}

func NewNixClient() *NixClient {
	return &NixClient{}
}

func WithAcceptFlakeConfig() imageOption {
	return func(o *imageOptions) { o.acceptFlakeConfig = true }
}

func WithNoPureEval() imageOption {
	return func(o *imageOptions) { o.noPureEval = true }
}

func makeImageOptions(opts ...imageOption) *imageOptions {
	o := &imageOptions{
		acceptFlakeConfig: true,
		noPureEval:        true,
	}
	for _, opt := range opts {
		opt(o)
	}
	return o
}

func (n *NixClient) GetImageBuilderType(
	ctx context.Context,
	buildContext string,
	ref name.Reference,
	p *v1.Platform,
	opts ...imageOption,
) (BuilderType, error) {
	o := makeImageOptions(opts...)

	args := []string{"flake", "show", "--json", "--all-systems", buildContext}
	if o.noPureEval {
		args = append(args, "--no-pure-eval")
	}
	cmd := nixCommandContext(ctx, "nix", args...)
	slog.DebugContext(ctx, "checking image builder type", "cmd", cmd.Path, "args", args)

	output, err := cmd.Output()
	if err != nil {
		return UnknownBuilderType, fmt.Errorf("failed to run nix flake show: %w", err)
	}

	var showOutput flakeShowOutput
	if err := json.Unmarshal(output, &showOutput); err != nil {
		return UnknownBuilderType, fmt.Errorf("failed to parse nix flake show output: %w", err)
	}

	system := formatSystemName(p)
	pkgName := formatNixFlakePackageName(ref)

	pkgs, ok := showOutput.Packages[system]
	if !ok {
		return UnknownBuilderType, fmt.Errorf("system %s not found in flake output", system)
	}

	pkg, ok := pkgs[pkgName]
	if !ok {
		return UnknownBuilderType, fmt.Errorf("package %s not found for system %s", pkgName, system)
	}

	if strings.HasPrefix(pkg.Name, "stream-") {
		slog.InfoContext(
			ctx,
			"resolved builder type",
			"ref",
			ref.Name(),
			"system",
			system,
			"package",
			pkgName,
			"builder_type",
			StreamBuilderType,
			"artifact_name",
			pkg.Name,
		)
		return StreamBuilderType, nil
	}
	if strings.HasSuffix(pkg.Name, ".tar.gz") {
		slog.InfoContext(
			ctx,
			"resolved builder type",
			"ref",
			ref.Name(),
			"system",
			system,
			"package",
			pkgName,
			"builder_type",
			TarGzBuilderType,
			"artifact_name",
			pkg.Name,
		)
		return TarGzBuilderType, nil
	}

	slog.WarnContext(
		ctx,
		"resolved builder type",
		"ref",
		ref.Name(),
		"system",
		system,
		"package",
		pkgName,
		"builder_type",
		UnknownBuilderType,
		"artifact_name",
		pkg.Name,
	)
	return UnknownBuilderType, nil
}

func (n *NixClient) BuildPlatformImage(
	ctx context.Context,
	buildContext string,
	ref name.Reference,
	p *v1.Platform,
	opts ...imageOption,
) (string, error) {
	return n.BuildImage(ctx, formatNixFlakePackage(buildContext, ref, p), opts...)
}

func (n *NixClient) BuildImage(
	ctx context.Context,
	url string,
	opts ...imageOption,
) (string, error) {
	o := makeImageOptions(opts...)

	args := []string{"build"}
	if o.acceptFlakeConfig {
		args = append(args, "--accept-flake-config", "--no-link")
	}
	args = append(args, "--json", url)
	cmd := nixCommandContext(ctx, "nix", args...)
	slog.InfoContext(ctx, "start nix build", "url", url, "args", args)

	stdoutPipe, err := cmd.StdoutPipe()
	if err != nil {
		return "", fmt.Errorf("failed to create stdout pipe: %w", err)
	}
	dec := json.NewDecoder(bufio.NewReader(stdoutPipe))

	stderrPipe, err := cmd.StderrPipe()
	if err != nil {
		return "", fmt.Errorf("failed to create stderr pipe: %w", err)
	}
	sc := bufio.NewScanner(stderrPipe)
	var stderrOutput strings.Builder
	var stderrMu sync.Mutex

	if err = cmd.Start(); err != nil {
		return "", fmt.Errorf("failed to run command: %w", err)
	}

	wg := errgroup.Group{}
	wg.Go(func() error {
		return handleNixBuild(sc, &stderrOutput, &stderrMu)
	})

	var result []*buildImageBuildResult
	if err := dec.Decode(&result); err != nil {
		return "", handleNixBuildError(
			ctx,
			url,
			fmt.Errorf("failed to parse nix build output: %w", err),
			&stderrOutput,
			&stderrMu,
		)
	}

	if err := wg.Wait(); err != nil {
		return "", handleNixBuildError(
			ctx,
			url,
			fmt.Errorf("failed to wait for command: %w", err),
			&stderrOutput,
			&stderrMu,
		)
	}
	if err := cmd.Wait(); err != nil {
		return "", handleNixBuildError(
			ctx,
			url,
			fmt.Errorf("failed to wait for command: %w", err),
			&stderrOutput,
			&stderrMu,
		)
	}

	if len(result) == 0 {
		return "", fmt.Errorf("no output path found in nix build result")
	}
	slog.DebugContext(
		ctx,
		"nix build completed",
		"url", url,
		"drvPath", result[0].DrvPath,
		"out", result[0].Outputs["out"],
	)
	slog.InfoContext(
		ctx,
		"nix build completed",
		"url",
		url,
		"drv_path",
		result[0].DrvPath,
		"out",
		result[0].Outputs["out"],
	)
	return result[0].Outputs["out"], nil
}

// fastBuildMessage is one JSON line of `nix-fast-build --stream-json-lines`.
// Only BUILD results carry the final outputs for an attribute.
type fastBuildMessage struct {
	Type    string            `json:"type"`
	Attr    string            `json:"attr"`
	Success bool              `json:"success"`
	Outputs map[string]string `json:"outputs"`
}

// BuildPlatformImages builds the flake package for every platform in a
// single nix-fast-build invocation and returns the store paths in platform
// order. Evaluation is shared (one nix-eval-jobs process) and builds fan
// out internally, so concurrent per-platform invocations can no longer race
// the shared eval-cache SQLite database — the root cause of doubled
// `packages.<system>.` attribute segments and "failed to parse nix build
// output: EOF" (see issue #98, previously mitigated by a mutex).
//
// The imageOption variadic exists for interface parity with the single
// build path; the current option set is fixed by nix-fast-build itself:
// accept-flake-config flows through --option, and --no-pure-eval is inert
// for flake evaluation.
func (n *NixClient) BuildPlatformImages(
	ctx context.Context,
	buildContext string,
	ref name.Reference,
	plats []*v1.Platform,
	opts ...imageOption,
) ([]string, error) {
	if len(plats) == 0 {
		return nil, fmt.Errorf("at least one platform is required")
	}

	pkgName := formatNixFlakePackageName(ref)
	attrs := make([]string, 0, len(plats))
	systems := make([]string, 0, len(plats))
	for _, p := range plats {
		system := formatSystemName(p)
		attrs = append(attrs, fmt.Sprintf("%s.%s", system, pkgName))
		systems = append(systems, system)
	}

	// The select must rebuild a NESTED attrset from real attrpath
	// segments: nix-eval-jobs requires an attrset traversal root, and
	// `builtins.getAttr "system.name"` (or `attrs ? system.name`) performs
	// a single-component lookup that never matches nested keys — such a
	// select silently filters out every job. Quoted segments keep any
	// package name valid; duplicates would error at eval (already covered
	// by distinct platforms).
	seen := make(map[string]bool, len(attrs))
	selectParts := make([]string, 0, len(attrs))
	for i, attr := range attrs {
		if seen[attr] {
			continue
		}
		seen[attr] = true
		selectParts = append(selectParts, fmt.Sprintf(
			`  %q.%q = attrs.%q.%q;`,
			systems[i], pkgName, systems[i], pkgName,
		))
	}
	selectExpr := "attrs: {\n" + strings.Join(selectParts, "\n") + "\n}"

	args := []string{
		"fast-build",
		"--flake", buildContext + "#packages",
		"--select", selectExpr,
		"--systems", strings.Join(systems, " "),
		"--option", "accept-flake-config", "true",
		"--stream-json-lines",
	}
	cmd := nixCommandContext(ctx, "nix", args...)
	slog.InfoContext(
		ctx,
		"start nix-fast-build",
		"flake",
		buildContext,
		"attrs",
		attrs,
		"args",
		args,
	)

	stdoutPipe, err := cmd.StdoutPipe()
	if err != nil {
		return nil, fmt.Errorf("failed to create stdout pipe: %w", err)
	}
	dec := json.NewDecoder(bufio.NewReader(stdoutPipe))

	stderrPipe, err := cmd.StderrPipe()
	if err != nil {
		return nil, fmt.Errorf("failed to create stderr pipe: %w", err)
	}
	sc := bufio.NewScanner(stderrPipe)
	var stderrOutput strings.Builder
	var stderrMu sync.Mutex

	if err = cmd.Start(); err != nil {
		return nil, fmt.Errorf("failed to run command: %w", err)
	}

	wg := errgroup.Group{}
	wg.Go(func() error {
		return handleNixBuild(sc, &stderrOutput, &stderrMu)
	})

	results := make(map[string]string, len(attrs))
	for {
		var msg fastBuildMessage
		if err := dec.Decode(&msg); err != nil {
			if errors.Is(err, io.EOF) {
				break
			}
			return nil, handleNixBuildError(
				ctx,
				buildContext,
				fmt.Errorf("failed to parse nix-fast-build output: %w", err),
				&stderrOutput,
				&stderrMu,
			)
		}
		if msg.Type != "BUILD" || !msg.Success {
			continue
		}
		out, ok := msg.Outputs["out"]
		if !ok {
			continue
		}
		results[msg.Attr] = out
	}

	if err := wg.Wait(); err != nil {
		return nil, handleNixBuildError(
			ctx,
			buildContext,
			fmt.Errorf("failed to wait for command: %w", err),
			&stderrOutput,
			&stderrMu,
		)
	}
	if err := cmd.Wait(); err != nil {
		err = handleNixBuildError(
			ctx,
			buildContext,
			fmt.Errorf("failed to wait for command: %w", err),
			&stderrOutput,
			&stderrMu,
		)
		var failed []string
		for _, attr := range attrs {
			if _, ok := results[attr]; !ok {
				failed = append(failed, attr)
			}
		}
		if len(failed) > 0 {
			err = fmt.Errorf("%w (failed attrs: %s)", err, strings.Join(failed, ", "))
		}
		return nil, err
	}

	paths := make([]string, 0, len(plats))
	for _, attr := range attrs {
		out, ok := results[attr]
		if !ok {
			return nil, fmt.Errorf("nix-fast-build produced no result for %s", attr)
		}
		paths = append(paths, out)
	}
	slog.InfoContext(ctx, "nix-fast-build completed", "flake", buildContext, "paths", paths)
	return paths, nil
}
