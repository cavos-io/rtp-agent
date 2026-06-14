package ten

import (
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
)

const (
	PluginTitle   = "rtp-agent.plugins.ten"
	PluginVersion = "1.0.0"
	PluginPackage = "rtp-agent.plugins.ten"

	modelFileName = "ten-vad.onnx"
	modelURL      = "https://raw.githubusercontent.com/TEN-framework/ten-vad/main/src/onnx_model/ten-vad.onnx"
)

var downloadModelFile = downloadFile

type Plugin struct{}

func (Plugin) DownloadFiles() error {
	path, err := ModelPath()
	if err != nil {
		return err
	}
	if info, err := os.Stat(path); err == nil && !info.IsDir() {
		return nil
	} else if err != nil && !os.IsNotExist(err) {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	if err := downloadModelFile(modelURL, path); err != nil {
		_ = os.Remove(path)
		return err
	}
	return nil
}

func ModelPath() (string, error) {
	cwd, err := os.Getwd()
	if err != nil {
		return "", err
	}
	return filepath.Join(cwd, "resources", "models", modelFileName), nil
}

func downloadFile(url, path string) error {
	resp, err := http.Get(url)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("download %s: unexpected status %s", url, resp.Status)
	}

	file, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o600)
	if err != nil {
		return err
	}
	defer file.Close()
	_, err = io.Copy(file, resp.Body)
	return err
}
