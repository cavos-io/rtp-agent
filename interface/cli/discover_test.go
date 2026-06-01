package cli

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

func TestGetDefaultPathFindsReferenceStyleEntrypoints(t *testing.T) {
	dir := t.TempDir()
	oldWD, err := os.Getwd()
	if err != nil {
		t.Fatalf("Getwd() error = %v", err)
	}
	if err := os.Chdir(dir); err != nil {
		t.Fatalf("Chdir(temp) error = %v", err)
	}
	defer os.Chdir(oldWD)

	if err := os.WriteFile(filepath.Join(dir, "agent.go"), []byte("package main\n"), 0o644); err != nil {
		t.Fatalf("write agent.go: %v", err)
	}

	got, err := GetDefaultPath()
	if err != nil {
		t.Fatalf("GetDefaultPath() error = %v", err)
	}
	if got != "agent.go" {
		t.Fatalf("GetDefaultPath() = %q, want agent.go", got)
	}
}

func TestGetDefaultPathPrefersMainBeforeAppAndAgent(t *testing.T) {
	dir := t.TempDir()
	oldWD, err := os.Getwd()
	if err != nil {
		t.Fatalf("Getwd() error = %v", err)
	}
	if err := os.Chdir(dir); err != nil {
		t.Fatalf("Chdir(temp) error = %v", err)
	}
	defer os.Chdir(oldWD)

	for _, name := range []string{"agent.go", "app.go", "main.go"} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte("package main\n"), 0o644); err != nil {
			t.Fatalf("write %s: %v", name, err)
		}
	}

	got, err := GetDefaultPath()
	if err != nil {
		t.Fatalf("GetDefaultPath() error = %v", err)
	}
	if got != "main.go" {
		t.Fatalf("GetDefaultPath() = %q, want main.go", got)
	}
}

func TestGetDefaultPathErrorsWhenNoEntrypointExists(t *testing.T) {
	dir := t.TempDir()
	oldWD, err := os.Getwd()
	if err != nil {
		t.Fatalf("Getwd() error = %v", err)
	}
	if err := os.Chdir(dir); err != nil {
		t.Fatalf("Chdir(temp) error = %v", err)
	}
	defer os.Chdir(oldWD)

	_, err = GetDefaultPath()
	if err == nil {
		t.Fatal("GetDefaultPath() error = nil, want missing default file error")
	}
}

func TestGetModuleDataFromPathDerivesGoImportMetadata(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "go.mod"), []byte("module example.com/agent\n\ngo 1.26\n"), 0o644); err != nil {
		t.Fatalf("write go.mod: %v", err)
	}
	cmdDir := filepath.Join(dir, "cmd", "worker")
	if err := os.MkdirAll(cmdDir, 0o755); err != nil {
		t.Fatalf("mkdir cmd/worker: %v", err)
	}
	mainPath := filepath.Join(cmdDir, "main.go")
	if err := os.WriteFile(mainPath, []byte("package main\n"), 0o644); err != nil {
		t.Fatalf("write main.go: %v", err)
	}

	data, err := GetModuleDataFromPath(mainPath)
	if err != nil {
		t.Fatalf("GetModuleDataFromPath() error = %v", err)
	}

	if data.ModuleImportString != "example.com/agent/cmd/worker" {
		t.Fatalf("ModuleImportString = %q, want example.com/agent/cmd/worker", data.ModuleImportString)
	}
	if data.ExtraSysPath != dir {
		t.Fatalf("ExtraSysPath = %q, want module root %q", data.ExtraSysPath, dir)
	}
	wantModulePaths := []string{dir, filepath.Join(dir, "cmd"), cmdDir}
	if !reflect.DeepEqual(data.ModulePaths, wantModulePaths) {
		t.Fatalf("ModulePaths = %#v, want %#v", data.ModulePaths, wantModulePaths)
	}
}

func TestGetImportDataUsesPackageName(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "go.mod"), []byte("module example.com/agent\n\ngo 1.26\n"), 0o644); err != nil {
		t.Fatalf("write go.mod: %v", err)
	}
	agentPath := filepath.Join(dir, "agent.go")
	if err := os.WriteFile(agentPath, []byte("package voiceagent\n"), 0o644); err != nil {
		t.Fatalf("write agent.go: %v", err)
	}

	data, err := GetImportData(agentPath)
	if err != nil {
		t.Fatalf("GetImportData() error = %v", err)
	}

	if data.AppName != "voiceagent" {
		t.Fatalf("AppName = %q, want package name voiceagent", data.AppName)
	}
	if data.ImportString != "example.com/agent:voiceagent" {
		t.Fatalf("ImportString = %q, want example.com/agent:voiceagent", data.ImportString)
	}
}
