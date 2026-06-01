package cli

import (
	"bufio"
	"fmt"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strings"

	"github.com/cavos-io/rtp-agent/library/logger"
)

var defaultEntrypointPaths = []string{
	"main.go",
	"app.go",
	"agent.go",
	filepath.Join("cmd", "main.go"),
	filepath.Join("cmd", "worker", "main.go"),
}

type ModuleData struct {
	ModuleImportString string
	ExtraSysPath       string
	ModulePaths        []string
}

type ImportData struct {
	AppName      string
	ModuleData   ModuleData
	ImportString string
}

func GetDefaultPath() (string, error) {
	for _, candidate := range defaultEntrypointPaths {
		info, err := os.Stat(candidate)
		if err == nil && !info.IsDir() {
			return candidate, nil
		}
		if err != nil && !os.IsNotExist(err) {
			return "", err
		}
	}
	return "", fmt.Errorf("could not find a default file to run, please provide an explicit path")
}

func GetModuleDataFromPath(path string) (ModuleData, error) {
	usePath, err := filepath.Abs(path)
	if err != nil {
		return ModuleData{}, err
	}
	info, err := os.Stat(usePath)
	if err != nil {
		return ModuleData{}, err
	}

	packagePath := usePath
	if !info.IsDir() {
		packagePath = filepath.Dir(usePath)
	}

	moduleRoot, modulePath, err := findGoModule(packagePath)
	if err != nil {
		return ModuleData{}, err
	}

	rel, err := filepath.Rel(moduleRoot, packagePath)
	if err != nil {
		return ModuleData{}, err
	}
	importString := modulePath
	if rel != "." {
		importString += "/" + filepath.ToSlash(rel)
	}

	modulePaths, err := modulePathChain(moduleRoot, packagePath)
	if err != nil {
		return ModuleData{}, err
	}

	return ModuleData{
		ModuleImportString: importString,
		ExtraSysPath:       moduleRoot,
		ModulePaths:        modulePaths,
	}, nil
}

func GetImportData(path string) (ImportData, error) {
	if path == "" {
		defaultPath, err := GetDefaultPath()
		if err != nil {
			return ImportData{}, err
		}
		path = defaultPath
	}
	if _, err := os.Stat(path); err != nil {
		if os.IsNotExist(err) {
			return ImportData{}, fmt.Errorf("path does not exist %s", path)
		}
		return ImportData{}, err
	}

	moduleData, err := GetModuleDataFromPath(path)
	if err != nil {
		return ImportData{}, err
	}
	appName, err := packageNameFromPath(path)
	if err != nil {
		return ImportData{}, err
	}
	return ImportData{
		AppName:      appName,
		ModuleData:   moduleData,
		ImportString: moduleData.ModuleImportString + ":" + appName,
	}, nil
}

// In Python, this dynamically imports plugins.
// In Go, since it is a compiled language, plugins are imported anonymously in main.go
// (e.g., _ "github.com/cavos-io/rtp-agent/adapter/openai").
// This function exists for structural parity.
func DiscoverPlugins() {
	_, _ = GetImportData("")
	logger.Logger.Debugw("Discovering plugins (compile-time in Go)")
	// Implement plugin registry checking here if a dynamic plugin system is added later.
}

func findGoModule(start string) (root string, modulePath string, err error) {
	for dir := start; ; dir = filepath.Dir(dir) {
		modulePath, err := readModulePath(filepath.Join(dir, "go.mod"))
		if err == nil {
			return dir, modulePath, nil
		}
		if !os.IsNotExist(err) {
			return "", "", err
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			break
		}
	}
	return "", "", fmt.Errorf("could not find go.mod for %s", start)
}

func readModulePath(path string) (string, error) {
	file, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer file.Close()

	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		fields := strings.Fields(scanner.Text())
		if len(fields) >= 2 && fields[0] == "module" {
			return fields[1], nil
		}
	}
	if err := scanner.Err(); err != nil {
		return "", err
	}
	return "", fmt.Errorf("module directive not found in %s", path)
}

func modulePathChain(root string, packagePath string) ([]string, error) {
	rel, err := filepath.Rel(root, packagePath)
	if err != nil {
		return nil, err
	}
	paths := []string{root}
	if rel == "." {
		return paths, nil
	}
	current := root
	for _, part := range strings.Split(rel, string(filepath.Separator)) {
		current = filepath.Join(current, part)
		paths = append(paths, current)
	}
	return paths, nil
}

func packageNameFromPath(path string) (string, error) {
	info, err := os.Stat(path)
	if err != nil {
		return "", err
	}
	fset := token.NewFileSet()
	if info.IsDir() {
		return packageNameFromDir(fset, path)
	}
	file, err := parser.ParseFile(fset, path, nil, parser.PackageClauseOnly)
	if err != nil {
		return "", err
	}
	return file.Name.Name, nil
}

func packageNameFromDir(fset *token.FileSet, path string) (string, error) {
	entries, err := os.ReadDir(path)
	if err != nil {
		return "", err
	}
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".go") || strings.HasSuffix(entry.Name(), "_test.go") {
			continue
		}
		file, err := parser.ParseFile(fset, filepath.Join(path, entry.Name()), nil, parser.PackageClauseOnly)
		if err != nil {
			return "", err
		}
		return file.Name.Name, nil
	}
	return "", fmt.Errorf("no Go package found in %s", path)
}
