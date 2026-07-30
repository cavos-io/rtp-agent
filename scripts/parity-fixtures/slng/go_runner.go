package main

import (
	"bufio"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

const (
	scenarioEnv    = "SLNG_PARITY_SCENARIO_PATH"
	outputSentinel = "SLNG_PARITY_OUTPUT="
)

func main() {
	path, cleanup, err := scenarioPath()
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(2)
	}
	defer cleanup()

	path, err = filepath.Abs(path)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(2)
	}
	command := exec.Command(
		"go", "test", "./adapter/slng",
		"-run", "^TestSLNGParityScenario$",
		"-count=1", "-v",
	)
	command.Env = append(os.Environ(), scenarioEnv+"="+path)
	output, err := command.CombinedOutput()
	if err != nil {
		os.Stderr.Write(output)
		os.Exit(1)
	}

	scanner := bufio.NewScanner(strings.NewReader(string(output)))
	scanner.Buffer(make([]byte, 1024), 1024*1024)
	for scanner.Scan() {
		if _, encoded, found := strings.Cut(scanner.Text(), outputSentinel); found {
			fmt.Println(encoded)
			return
		}
	}
	if err := scanner.Err(); err != nil {
		fmt.Fprintln(os.Stderr, err)
	} else {
		fmt.Fprintln(os.Stderr, "SLNG parity test emitted no JSON")
	}
	os.Stderr.Write(output)
	os.Exit(1)
}

func scenarioPath() (string, func(), error) {
	switch len(os.Args) {
	case 1:
		file, err := os.CreateTemp("", "slng-parity-*.json")
		if err != nil {
			return "", func() {}, err
		}
		cleanup := func() { _ = os.Remove(file.Name()) }
		if _, err := io.Copy(file, os.Stdin); err != nil {
			_ = file.Close()
			cleanup()
			return "", func() {}, err
		}
		if err := file.Close(); err != nil {
			cleanup()
			return "", func() {}, err
		}
		return file.Name(), cleanup, nil
	case 2:
		return os.Args[1], func() {}, nil
	default:
		return "", func() {}, fmt.Errorf("usage: go_runner [SCENARIO_JSON]")
	}
}
