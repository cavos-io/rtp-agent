package cli

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/cavos-io/conversation-worker/library/logger"
	"github.com/fsnotify/fsnotify"
)

type Watcher struct {
	paths    []string
	onChange func()
	watcher  *fsnotify.Watcher
	mu       sync.Mutex
	ctx      context.Context
	cancel   context.CancelFunc
}

func NewWatcher(paths []string, onChange func()) *Watcher {
	return &Watcher{
		paths:    paths,
		onChange: onChange,
	}
}

func (w *Watcher) Start() error {
	watcher, err := fsnotify.NewWatcher()
	if err != nil {
		return err
	}
	w.watcher = watcher

	for _, path := range w.paths {
		err = filepath.Walk(path, func(p string, info os.FileInfo, err error) error {
			if err != nil {
				return err
			}
			if info.IsDir() {
				if strings.HasPrefix(info.Name(), ".") && info.Name() != "." {
					return filepath.SkipDir
				}
				return watcher.Add(p)
			}
			return nil
		})
		if err != nil {
			logger.Logger.Warnw("failed to add path to watcher", err, "path", path)
		}
	}

	w.ctx, w.cancel = context.WithCancel(context.Background())

	go w.watchLoop()

	return nil
}

func (w *Watcher) Stop() error {
	if w.cancel != nil {
		w.cancel()
	}
	if w.watcher != nil {
		return w.watcher.Close()
	}
	return nil
}

func (w *Watcher) watchLoop() {
	// Debounce events
	var timer *time.Timer
	for {
		select {
		case <-w.ctx.Done():
			return
		case event, ok := <-w.watcher.Events:
			if !ok {
				return
			}
			if event.Op&fsnotify.Write == fsnotify.Write || event.Op&fsnotify.Create == fsnotify.Create {
				if strings.HasSuffix(event.Name, ".go") {
					if timer != nil {
						timer.Stop()
					}
					timer = time.AfterFunc(500*time.Millisecond, func() {
						logger.Logger.Infow("File changed, triggering reload", "file", event.Name)
						if w.onChange != nil {
							w.onChange()
						}
					})
				}
			}
		case err, ok := <-w.watcher.Errors:
			if !ok {
				return
			}
			logger.Logger.Errorw("Watcher error", err)
		}
	}
}

func RunWithDevMode(args []string) error {
	var cmd *exec.Cmd
	var mu sync.Mutex

	startCmd := func() {
		mu.Lock()
		defer mu.Unlock()

		if cmd != nil && cmd.Process != nil {
			logger.Logger.Infow("Stopping current process")
			cmd.Process.Kill()
			cmd.Wait()
		}

		logger.Logger.Infow("Starting process via go run")

		// Build the args for `go run`
		goArgs := []string{"run", "cmd/main.go", "start"} // Assuming standard layout

		cmd = exec.Command("go", goArgs...)
		cmd.Stdout = os.Stdout
		cmd.Stderr = os.Stderr
		cmd.Stdin = os.Stdin

		err := cmd.Start()
		if err != nil {
			logger.Logger.Errorw("Failed to start process", err)
		}
	}

	w := NewWatcher([]string{"./"}, func() {
		logger.Logger.Infow("Triggering rebuild and restart")
		startCmd()
	})

	if err := w.Start(); err != nil {
		return err
	}
	defer w.Stop()

	startCmd()

	// Wait forever
	select {}
}
