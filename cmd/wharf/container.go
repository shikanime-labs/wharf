package main

import (
	"compress/gzip"
	"context"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"strings"

	"github.com/google/go-containerregistry/pkg/authn"
	"github.com/google/go-containerregistry/pkg/name"
	v1 "github.com/google/go-containerregistry/pkg/v1"
	"github.com/google/go-containerregistry/pkg/v1/empty"
	"github.com/google/go-containerregistry/pkg/v1/mutate"
	"github.com/google/go-containerregistry/pkg/v1/remote"
	"github.com/google/go-containerregistry/pkg/v1/tarball"
)

type ContainerOption func(*containerOptions)

type containerOptions struct {
	keychain  authn.Keychain
	transport http.RoundTripper
	remote    []remote.Option
}

type ContainerClient struct {
	keychain  authn.Keychain
	transport http.RoundTripper
	remote    []remote.Option
}

func WithContainerKeychain(kc authn.Keychain) ContainerOption {
	return func(o *containerOptions) {
		o.keychain = kc
		o.remote = append(o.remote, remote.WithAuthFromKeychain(kc))
	}
}

func WithContainerTransport(t http.RoundTripper) ContainerOption {
	return func(o *containerOptions) {
		o.transport = t
		o.remote = append(o.remote, remote.WithTransport(t))
	}
}

func WithContainerRemoteOption(opt remote.Option) ContainerOption {
	return func(o *containerOptions) {
		o.remote = append(o.remote, opt)
	}
}

func makeContainerOptions(opts ...ContainerOption) *containerOptions {
	o := &containerOptions{
		keychain:  authn.DefaultKeychain,
		transport: http.DefaultTransport,
	}
	o.remote = append(o.remote, remote.WithAuthFromKeychain(o.keychain))
	o.remote = append(o.remote, remote.WithTransport(o.transport))
	for _, opt := range opts {
		opt(o)
	}
	return o
}

func NewContainerClient(_ context.Context, opts ...ContainerOption) *ContainerClient {
	o := makeContainerOptions(opts...)
	return &ContainerClient{
		keychain:  o.keychain,
		transport: o.transport,
		remote:    o.remote,
	}
}

func (c *ContainerClient) CheckPushPermission(ref name.Reference) error {
	if err := remote.CheckPushPermission(ref, c.keychain, c.transport); err != nil {
		return fmt.Errorf("check push permission failed: %w", err)
	}
	return nil
}

// PushImage pushes a nix-built image tarball straight to the registry.
// It reads the tarball from disk and never touches the docker daemon — the
// daemon load/tag step was redundant (the tarball is already complete) and
// broke on docker 28's load-output parsing.
func (c *ContainerClient) PushImage(ref name.Reference, path string) error {
	img, err := tarball.Image(gzipPathOpener(path), nil)
	if err != nil {
		return fmt.Errorf("load image from tarball failed: %w", err)
	}
	if err := remote.Write(ref, img, c.remote...); err != nil {
		return fmt.Errorf("push image failed: %w", err)
	}
	return nil
}

func (c *ContainerClient) PushPlatformImage(
	ref name.Reference,
	p *v1.Platform,
	path string,
) (mutate.IndexAddendum, error) {
	img, err := tarball.Image(gzipPathOpener(path), nil)
	if err != nil {
		return mutate.IndexAddendum{}, fmt.Errorf("load image from tarball failed: %w", err)
	}
	if err := remote.Write(ref, img, c.remote...); err != nil {
		return mutate.IndexAddendum{}, fmt.Errorf("push image failed: %w", err)
	}
	return mutate.IndexAddendum{
		Add:        img,
		Descriptor: v1.Descriptor{Platform: p},
	}, nil
}

func (c *ContainerClient) PushManifest(
	ref name.Reference,
	adds []mutate.IndexAddendum,
) error {
	if err := remote.WriteIndex(
		ref,
		mutate.AppendManifests(empty.Index, adds...),
		c.remote...,
	); err != nil {
		return fmt.Errorf("push manifest failed: %w", err)
	}
	return nil
}

func gzipPathOpener(path string) tarball.Opener {
	return func() (io.ReadCloser, error) {
		// streamLayeredImage output is an executable wrapper that writes the
		// image tarball to stdout (it execs dockerTools' `stream` helper).
		// Opening it as a file yields garbage → "unexpected EOF" at
		// tarball.Image. Run it and stream stdout instead.
		if fi, err := os.Stat(path); err == nil && fi.Mode()&0o111 != 0 {
			cmd := exec.CommandContext(
				context.Background(),
				path,
			) //nolint:gosec // path is a nix store derivation output
			out, err := cmd.StdoutPipe()
			if err != nil {
				return nil, err
			}
			var stderr strings.Builder
			cmd.Stderr = &stderr
			if err := cmd.Start(); err != nil {
				return nil, err
			}
			return &streamOpener{Reader: out, cmd: cmd, stderr: &stderr}, nil
		}
		f, err := os.Open(path)
		if err != nil {
			return nil, err
		}
		if strings.HasSuffix(path, ".tar.gz") {
			gr, err := gzip.NewReader(f)
			if err != nil {
				_ = f.Close()
				return nil, err
			}
			return &gzipReadCloser{Reader: gr, file: f}, nil
		}
		return f, nil
	}
}

// streamOpener wraps a command's stdout pipe; Close waits for the process so
// a truncated stream (non-zero exit) surfaces as an error instead of a silent EOF.
type streamOpener struct {
	io.Reader
	cmd    *exec.Cmd
	stderr *strings.Builder
}

func (c *streamOpener) Close() error {
	if err := c.cmd.Wait(); err != nil {
		msg := strings.TrimSpace(c.stderr.String())
		if msg != "" {
			return fmt.Errorf("%w: %s", err, msg)
		}
		return err
	}
	return nil
}

type gzipReadCloser struct {
	*gzip.Reader
	file *os.File
}

func (g *gzipReadCloser) Close() error {
	if err := g.Reader.Close(); err != nil {
		return err
	}
	return g.file.Close()
}
