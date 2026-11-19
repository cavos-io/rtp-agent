package nativeroom

import (
	"archive/zip"
	"bytes"
	"context"
	"crypto/sha256"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/cavos-io/rtp-agent/library/plugin"
)

func TestNativeDownloadPlatformAndRegistration(t *testing.T) {
	for _, platform := range []struct{ os, arch, name string }{
		{"linux", "amd64", "ffi-linux-x86_64.zip"}, {"linux", "arm64", "ffi-linux-arm64.zip"},
		{"darwin", "amd64", "ffi-macos-x86_64.zip"}, {"darwin", "arm64", "ffi-macos-arm64.zip"},
	} {
		a, err := nativeArtifact(platform.os, platform.arch)
		if err != nil || a.name != platform.name || len(a.checksum) != 64 {
			t.Fatalf("artifact: %+v %v", a, err)
		}
	}
	if _, err := nativeArtifact("windows", "amd64"); err == nil {
		t.Fatal("unsupported platform accepted")
	}
	found := false
	for _, p := range plugin.RegisteredPlugins() {
		if p.Package() == nativePluginPackage {
			found = true
			if p.Version() != "0.12.80" {
				t.Fatal(p.Version())
			}
		}
	}
	if !found {
		t.Fatal("native downloader not registered")
	}
}

func TestNativeLibraryPath(t *testing.T) {
	t.Chdir(t.TempDir())
	t.Setenv("LIVEKIT_LIB_PATH", "")
	p, err := NativeLibraryPath()
	if err != nil || filepath.Dir(p) != filepath.Join(mustCWD(t), "resources", "lib", "livekit") {
		t.Fatalf("path %q: %v", p, err)
	}
	t.Setenv("LIVEKIT_LIB_PATH", "custom/library")
	p, err = NativeLibraryPath()
	if err != nil || p != filepath.Join(mustCWD(t), "custom/library") {
		t.Fatalf("override %q: %v", p, err)
	}
}
func mustCWD(t *testing.T) string {
	t.Helper()
	p, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	return p
}

func TestNativeDownloadInstallAndFailurePreservation(t *testing.T) {
	var archive bytes.Buffer
	z := zip.NewWriter(&archive)
	f, err := z.Create("bundle/liblivekit_ffi.so")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.Write([]byte("native-library")); err != nil {
		t.Fatal(err)
	}
	if err := z.Close(); err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { _, _ = w.Write(archive.Bytes()) }))
	defer server.Close()
	a := ffiArtifact{name: "test.zip", library: "liblivekit_ffi.so", checksum: fmt.Sprintf("%x", sha256.Sum256(archive.Bytes()))}
	dest := filepath.Join(t.TempDir(), "lib", "liblivekit_ffi.so")
	if err := downloadNativeLibrary(context.Background(), server.Client(), server.URL, a, dest); err != nil {
		t.Fatal(err)
	}
	assertInstalled := func() {
		t.Helper()
		b, err := os.ReadFile(dest)
		if err != nil || string(b) != "native-library" {
			t.Fatalf("installed %q: %v", b, err)
		}
	}
	assertInstalled()
	a.checksum = "incorrect"
	if err := downloadNativeLibrary(context.Background(), server.Client(), server.URL, a, dest); err == nil {
		t.Fatal("checksum mismatch accepted")
	}
	assertInstalled()
	a.checksum = fmt.Sprintf("%x", sha256.Sum256(archive.Bytes()))
	a.library = "missing.so"
	if err := downloadNativeLibrary(context.Background(), server.Client(), server.URL, a, dest); err == nil {
		t.Fatal("missing library accepted")
	}
	assertInstalled()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := downloadNativeLibrary(ctx, server.Client(), server.URL, a, dest); err == nil {
		t.Fatal("cancellation ignored")
	}
	assertInstalled()
}

func TestNativeDownloadRejectsBadArchivesAndHTTP(t *testing.T) {
	for _, tc := range []struct {
		name    string
		status  int
		entries []string
		invalid bool
	}{
		{name: "HTTP failure", status: 503},
		{name: "invalid zip", status: 200, invalid: true},
		{name: "duplicate libraries", status: 200, entries: []string{"a/liblivekit_ffi.so", "b/liblivekit_ffi.so"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var data bytes.Buffer
			if tc.invalid {
				data.WriteString("not a zip")
			} else {
				z := zip.NewWriter(&data)
				for _, name := range tc.entries {
					f, err := z.Create(name)
					if err != nil {
						t.Fatal(err)
					}
					if _, err := f.Write([]byte("library")); err != nil {
						t.Fatal(err)
					}
				}
				if err := z.Close(); err != nil {
					t.Fatal(err)
				}
			}
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(tc.status); _, _ = w.Write(data.Bytes()) }))
			defer server.Close()
			dest := filepath.Join(t.TempDir(), "library")
			if err := os.WriteFile(dest, []byte("existing"), 0600); err != nil {
				t.Fatal(err)
			}
			a := ffiArtifact{library: "liblivekit_ffi.so", checksum: fmt.Sprintf("%x", sha256.Sum256(data.Bytes()))}
			if err := downloadNativeLibrary(context.Background(), server.Client(), server.URL, a, dest); err == nil {
				t.Fatal("invalid response accepted")
			}
			b, err := os.ReadFile(dest)
			if err != nil || string(b) != "existing" {
				t.Fatalf("existing library changed: %q %v", b, err)
			}
			entries, err := os.ReadDir(filepath.Dir(dest))
			if err != nil || len(entries) != 1 {
				t.Fatalf("temporary files left: %v %v", entries, err)
			}
		})
	}
}
