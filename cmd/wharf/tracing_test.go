package main

import (
	"context"
	"testing"
)

func TestSetupTracingReturnsStop(t *testing.T) {
	stop := setupTracing(context.Background())
	if stop == nil {
		t.Fatal("stop func must not be nil")
	}
	if err := stop(context.Background()); err != nil {
		t.Fatalf("stop returned error: %v", err)
	}
}

func TestStartSpanReturnsSpan(t *testing.T) {
	_, span := startSpan(context.Background(), "nix.build.image")
	if span == nil {
		t.Fatal("startSpan must return a non-nil span")
	}
	span.End()
}
