package nativeroom

import (
	"archive/zip"
	"context"
	"crypto/sha256"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"time"

	"github.com/cavos-io/rtp-agent/library/plugin"
)

const nativeFFIVersion = "0.12.80"
const nativePluginPackage = "rtp-agent.transport.livekit-native"

type ffiArtifact struct{ name, library, checksum string }

func nativeArtifact(goos, arch string) (ffiArtifact, error) {
	artifacts := map[string]ffiArtifact{
		"linux/amd64":  {"ffi-linux-x86_64.zip", "liblivekit_ffi.so", "81d1daa1543e25792a0dafe246d1c270611875bfc2ef544fe9be1fbafdfc77d5"},
		"linux/arm64":  {"ffi-linux-arm64.zip", "liblivekit_ffi.so", "28cde1cb0110ed5c0ff645c8ee20321c8e6786425f4ee8b5a12c530c62c4ab4b"},
		"darwin/amd64": {"ffi-macos-x86_64.zip", "liblivekit_ffi.dylib", "fe823e7b9121f971ac1bb21e274611751fe03fdc19207ff412848b670bdfe764"},
		"darwin/arm64": {"ffi-macos-arm64.zip", "liblivekit_ffi.dylib", "980ae8b1dc687427235ce8de503068d77c6461d383beacb94592eeb2b5595386"},
	}
	a, ok := artifacts[goos+"/"+arch]
	if !ok {
		return a, fmt.Errorf("native FFI unsupported platform %s/%s", goos, arch)
	}
	return a, nil
}

func init() {
	plugin.RegisterPluginDownloader(nativePluginPackage, nativeFFIVersion, nativePluginPackage, func() error { return DownloadNativeLibrary(context.Background()) })
}

// NativeLibraryPath resolves the explicit override or the application-local install.
// It does not download or load the library.
func NativeLibraryPath() (string, error) {
	if path := os.Getenv("LIVEKIT_LIB_PATH"); path != "" {
		return filepath.Abs(path)
	}
	a, err := nativeArtifact(runtime.GOOS, runtime.GOARCH)
	if err != nil {
		return "", err
	}
	return filepath.Abs(filepath.Join("resources", "lib", "livekit", a.library))
}

// DownloadNativeLibrary installs the pinned runtime, verifying the archive before
// atomically replacing the destination. It never changes transport selection.
func DownloadNativeLibrary(ctx context.Context) error {
	a, err := nativeArtifact(runtime.GOOS, runtime.GOARCH)
	if err != nil {
		return err
	}
	dest, err := NativeLibraryPath()
	if err != nil {
		return err
	}
	url := "https://github.com/livekit/rust-sdks/releases/download/livekit-ffi/v" + nativeFFIVersion + "/" + a.name
	if err := downloadNativeLibrary(ctx, &http.Client{Timeout: 10 * time.Minute}, url, a, dest); err != nil {
		return err
	}
	fmt.Printf("LiveKit FFI %s installed in %s\n", nativeFFIVersion, dest)
	return nil
}

func downloadNativeLibrary(ctx context.Context, client *http.Client, url string, a ffiArtifact, dest string) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return err
	}
	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("download native FFI: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("download native FFI: HTTP %d", resp.StatusCode)
	}
	archive, err := os.CreateTemp("", "livekit-ffi-*.zip")
	if err != nil {
		return err
	}
	defer os.Remove(archive.Name())
	defer archive.Close()
	hash := sha256.New()
	// Bound both download and extracted size; never unpack arbitrary archive paths.
	const maxSize = 512 << 20
	size, err := io.Copy(io.MultiWriter(archive, hash), io.LimitReader(resp.Body, maxSize+1))
	if err != nil {
		return err
	}
	if size > maxSize {
		return fmt.Errorf("native FFI archive exceeds size limit")
	}
	if fmt.Sprintf("%x", hash.Sum(nil)) != a.checksum {
		return fmt.Errorf("native FFI archive checksum mismatch")
	}
	reader, err := zip.NewReader(archive, size)
	if err != nil {
		return err
	}
	var library *zip.File
	for _, file := range reader.File {
		if filepath.Base(file.Name) != a.library {
			continue
		}
		if library != nil || !file.Mode().IsRegular() || file.UncompressedSize64 > maxSize {
			return fmt.Errorf("invalid native library archive entry")
		}
		library = file
	}
	if library == nil {
		return fmt.Errorf("native library %s missing from archive", a.library)
	}
	input, err := library.Open()
	if err != nil {
		return err
	}
	defer input.Close()
	if err := os.MkdirAll(filepath.Dir(dest), 0755); err != nil {
		return err
	}
	stage, err := os.CreateTemp(filepath.Dir(dest), ".livekit-ffi-*")
	if err != nil {
		return err
	}
	defer os.Remove(stage.Name())
	defer stage.Close()
	size, err = io.Copy(stage, io.LimitReader(input, maxSize+1))
	if err != nil {
		return err
	}
	if size > maxSize {
		return fmt.Errorf("native library exceeds size limit")
	}
	if err := stage.Chmod(0755); err != nil {
		return err
	}
	if err := stage.Close(); err != nil {
		return err
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	return os.Rename(stage.Name(), dest)
}
