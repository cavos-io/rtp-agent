package livekit

import (
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
)

const (
	PluginTitle   = "rtp-agent.plugins.livekit"
	PluginVersion = "1.5.15"
	PluginPackage = "rtp-agent.plugins.livekit"
)

type Plugin struct{}

var downloadTurnDetectorFile = downloadFile

func (Plugin) DownloadFiles() error {
	for _, modelType := range []ModelType{ModelEnglish, ModelMultilingual} {
		files, err := turnDetectorDownloadFiles(modelType)
		if err != nil {
			return err
		}
		for _, file := range files {
			if info, err := os.Stat(file.path); err == nil && !info.IsDir() {
				continue
			} else if err != nil && !os.IsNotExist(err) {
				return err
			}
			if err := os.MkdirAll(filepath.Dir(file.path), 0o755); err != nil {
				return err
			}
			if err := downloadTurnDetectorFile(file.url, file.path); err != nil {
				_ = os.Remove(file.path)
				return err
			}
		}
	}
	return nil
}

type turnDetectorDownloadFile struct {
	url  string
	path string
}

func ModelONNXPath(modelType ModelType) (string, error) {
	cwd, err := os.Getwd()
	if err != nil {
		return "", err
	}
	return ModelONNXPathIn(cwd, modelType), nil
}

func ModelONNXPathIn(rootDir string, modelType ModelType) string {
	return filepath.Join(modelResourceDir(rootDir, modelType), "onnx", ONNXFilename)
}

func ModelTokenizerPath(modelType ModelType) (string, error) {
	cwd, err := os.Getwd()
	if err != nil {
		return "", err
	}
	return ModelTokenizerPathIn(cwd, modelType), nil
}

func ModelTokenizerPathIn(rootDir string, modelType ModelType) string {
	return filepath.Join(modelResourceDir(rootDir, modelType), "tokenizer.json")
}

func ModelLanguagesPath(modelType ModelType) (string, error) {
	cwd, err := os.Getwd()
	if err != nil {
		return "", err
	}
	return ModelLanguagesPathIn(cwd, modelType), nil
}

func ModelLanguagesPathIn(rootDir string, modelType ModelType) string {
	return filepath.Join(modelResourceDir(rootDir, modelType), "languages.json")
}

func modelResourceDir(rootDir string, modelType ModelType) string {
	return filepath.Join(rootDir, "resources", "models", "livekit", "turn-detector", modelRevisions[modelType])
}

func turnDetectorDownloadFiles(modelType ModelType) ([]turnDetectorDownloadFile, error) {
	revision := modelRevisions[modelType]
	onnxPath, err := ModelONNXPath(modelType)
	if err != nil {
		return nil, err
	}
	tokenizerPath, err := ModelTokenizerPath(modelType)
	if err != nil {
		return nil, err
	}
	languagesPath, err := ModelLanguagesPath(modelType)
	if err != nil {
		return nil, err
	}
	return []turnDetectorDownloadFile{
		{
			url:  huggingFaceResolveURL(revision, "onnx/"+ONNXFilename),
			path: onnxPath,
		},
		{
			url:  huggingFaceResolveURL(revision, "tokenizer.json"),
			path: tokenizerPath,
		},
		{
			url:  huggingFaceResolveURL(revision, "languages.json"),
			path: languagesPath,
		},
	}, nil
}

func huggingFaceResolveURL(revision string, filename string) string {
	return fmt.Sprintf("https://huggingface.co/%s/resolve/%s/%s", HGModel, revision, filename)
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
