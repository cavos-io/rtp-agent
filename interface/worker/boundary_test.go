package worker

import (
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestWorkerProductionCodeUsesLiveKitSubpackageForLiveKitImports(t *testing.T) {
	root := "."
	forbidden := []string{
		`"github.com/livekit/`,
		`lkprotocol "github.com/livekit/`,
		`lksdk "github.com/livekit/`,
		`auth "github.com/livekit/`,
	}

	err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() {
			if path == "livekit" {
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return nil
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		for _, forbiddenImport := range forbidden {
			if strings.Contains(string(data), forbiddenImport) {
				t.Fatalf("%s imports %s; route LiveKit SDK/protocol usage through interface/worker/livekit", path, forbiddenImport)
			}
		}
		return nil
	})
	if err != nil {
		t.Fatalf("walk worker files: %v", err)
	}
}

func TestSharedWorkerDoesNotBuildLiveKitStatusMessagesDirectly(t *testing.T) {
	forbiddenCalls := []string{
		"workerlivekit.ApplyWorkerEnv(",
		"workerlivekit.ExchangeInitialRegisterWebSocket(",
		"workerlivekit.WorkerStatusUpdateMessage(",
		"workerlivekit.RegisterWorkerMessage(",
		"workerlivekit.ValidateWorkerConnectionOptions(",
	}

	err := filepath.WalkDir(".", func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() {
			if path == "livekit" {
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return nil
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		for _, forbiddenCall := range forbiddenCalls {
			if strings.Contains(string(data), forbiddenCall) {
				t.Fatalf("%s calls %s; use the LiveKit server message facade", path, forbiddenCall)
			}
		}
		return nil
	})
	if err != nil {
		t.Fatalf("walk worker files: %v", err)
	}
}
