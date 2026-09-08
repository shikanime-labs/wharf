package main

import (
	"io"
	"os"
	"path/filepath"
	"testing"
)

// TestGzipPathOpenerRunsExecutableWrapper proves the opener execs a
// streamLayeredImage wrapper (executable) and streams its stdout, rather than
// opening the wrapper file as a tarball (which yields unexpected EOF).
func TestGzipPathOpenerRunsExecutableWrapper(t *testing.T) {
	dir := t.TempDir()
	wrapper := filepath.Join(dir, "img")
	// Wrapper that writes a known marker to stdout, proving it was executed
	// (a real streamLayeredImage wrapper emits the image tarball here).
	script := "#!/bin/sh\nprintf 'MARKER_FROM_WRAPPER'\n"
	if err := os.WriteFile(wrapper, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}

	rc, err := gzipPathOpener(wrapper)()
	if err != nil {
		t.Fatalf("opener returned error: %v", err)
	}
	defer closeRC(t, rc)

	buf := make([]byte, 64)
	n, err := io.ReadFull(rc, buf)
	if err != nil && err != io.ErrUnexpectedEOF {
		t.Fatalf("read wrapper stdout: %v", err)
	}
	if string(buf[:n]) != "MARKER_FROM_WRAPPER" {
		t.Fatalf("wrapper was not executed; got %q", string(buf[:n]))
	}
}

// TestGzipPathOpenerReadsPlainTar confirms non-executable files still open as a file.
func TestGzipPathOpenerReadsPlainTar(t *testing.T) {
	dir := t.TempDir()
	tarPath := filepath.Join(dir, "img.tar")
	if err := os.WriteFile(tarPath, []byte("ustar\x0000"), 0o644); err != nil {
		t.Fatal(err)
	}
	rc, err := gzipPathOpener(tarPath)()
	if err != nil {
		t.Fatalf("opener returned error: %v", err)
	}
	defer closeRC(t, rc)
	buf := make([]byte, 8)
	if _, err := io.ReadFull(rc, buf); err != nil {
		t.Fatalf("read plain tar: %v", err)
	}
}

// closeRC discards Close errors (errcheck); a nonzero exit surfaces via the
// read, not the close.
func closeRC(t *testing.T, rc io.ReadCloser) {
	t.Helper()
	if err := rc.Close(); err != nil {
		t.Logf("close: %v", err)
	}
}
