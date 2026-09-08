package main

import (
	"testing"

	"github.com/google/go-containerregistry/pkg/name"
	v1 "github.com/google/go-containerregistry/pkg/v1"
)

func TestFormatSystemName(t *testing.T) {
	got := formatSystemName(&v1.Platform{OS: "linux", Architecture: "arm64"})
	if got != "aarch64-linux" {
		t.Fatalf("expected aarch64-linux, got %s", got)
	}
}

func TestFormatNixFlakePackage(t *testing.T) {
	ref, err := name.ParseReference("ghcr.io/shikanime/shikanime/catbox:latest")
	if err != nil {
		t.Fatalf("parse reference failed: %v", err)
	}

	got := formatNixFlakePackage(
		"/workspace",
		ref,
		&v1.Platform{OS: "linux", Architecture: "amd64"},
	)
	want := "/workspace#packages.x86_64-linux.catbox"
	if got != want {
		t.Fatalf("expected %s, got %s", want, got)
	}
}

func TestResolveFlakeURL(t *testing.T) {
	tests := []struct {
		name         string
		buildContext string
		flake        string
		expected     string
		wantErr      bool
	}{
		{
			name:         "flake URL overrides build context",
			buildContext: "/workspace",
			flake:        "github:shikanime-labs/nix-containers",
			expected:     "github:shikanime-labs/nix-containers",
		},
		{
			name:         "empty flake falls back to build context",
			buildContext: "/workspace",
			flake:        "",
			expected:     "/workspace",
		},
		{
			name:         "both empty yields empty",
			buildContext: "",
			flake:        "",
			expected:     "",
		},
		{
			name:         "whitespace-only flake falls back to build context",
			buildContext: "/workspace",
			flake:        "   ",
			expected:     "/workspace",
		},
		{
			name:     "dash-prefixed flake is rejected",
			flake:    "--impure",
			wantErr:  true,
			expected: "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := resolveFlakeURL(tt.buildContext, tt.flake)
			if tt.wantErr {
				if err == nil {
					t.Fatalf("expected error, got %q", got)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if got != tt.expected {
				t.Fatalf("expected %s, got %s", tt.expected, got)
			}
		})
	}
}
