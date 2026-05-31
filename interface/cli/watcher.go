package cli

import (
	"context"
	"errors"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/cavos-io/conversation-worker/interface/worker/ipc"
	"github.com/cavos-io/conversation-worker/library/logger"
	"github.com/fsnotify/fsnotify"
)

type Watcher struct {
	paths      []string
	onChange   func()
	watcher    *fsnotify.Watcher
	mu         sync.Mutex
	ctx        context.Context
	cancel     context.CancelFunc
	reloading  bool
	cliArgs    *CliArgs
	activeJobs []ipc.RunningJobInfo
	reloadIPC  io.Writer
	reloadJobs chan struct{}

	activeJobsTimeout time.Duration
}

func NewWatcher(paths []string, onChange func(), cliArgs ...*CliArgs) *Watcher {
	watcher := &Watcher{
		paths:             paths,
		onChange:          onChange,
		activeJobsTimeout: 1500 * time.Millisecond,
	}
	if len(cliArgs) > 0 {
		watcher.cliArgs = cliArgs[0]
	}
	return watcher
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

func (w *Watcher) beginReload() bool {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.reloading {
		return false
	}
	w.reloading = true
	if w.cliArgs != nil {
		w.cliArgs.ReloadCount++
	}
	return true
}

func (w *Watcher) markReloading() bool {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.reloading {
		return false
	}
	w.reloading = true
	return true
}

func (w *Watcher) incrementReloadCount() {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.cliArgs != nil {
		w.cliArgs.ReloadCount++
	}
}

func (w *Watcher) beginActiveJobsWait() <-chan struct{} {
	w.mu.Lock()
	defer w.mu.Unlock()
	ch := make(chan struct{})
	w.reloadJobs = ch
	return ch
}

func (w *Watcher) clearActiveJobsWait(ch <-chan struct{}) {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.reloadJobs == ch {
		w.reloadJobs = nil
	}
}

func (w *Watcher) waitForActiveJobs(ch <-chan struct{}) {
	if ch == nil {
		return
	}
	timeout := w.activeJobsTimeout
	if timeout <= 0 {
		w.clearActiveJobsWait(ch)
		return
	}
	timer := time.NewTimer(timeout)
	defer timer.Stop()
	select {
	case <-ch:
	case <-timer.C:
	}
	w.clearActiveJobsWait(ch)
}

func (w *Watcher) Reloaded() {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.reloading = false
}

func (w *Watcher) recordActiveJobsResponse(resp ipc.ActiveJobsResponse) bool {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.cliArgs != nil && resp.ReloadCount != w.cliArgs.ReloadCount {
		return false
	}
	w.activeJobs = append([]ipc.RunningJobInfo(nil), resp.Jobs...)
	if w.reloadJobs != nil {
		close(w.reloadJobs)
		w.reloadJobs = nil
	}
	return true
}

func (w *Watcher) reloadJobsResponse() ipc.ReloadJobsResponse {
	w.mu.Lock()
	defer w.mu.Unlock()
	reloadCount := 0
	if w.cliArgs != nil {
		reloadCount = w.cliArgs.ReloadCount
	}
	return ipc.ReloadJobsResponse{
		Jobs:        append([]ipc.RunningJobInfo(nil), w.activeJobs...),
		ReloadCount: reloadCount,
	}
}

func (w *Watcher) handleReloadMessage(payload any) (any, bool) {
	switch msg := payload.(type) {
	case *ipc.ActiveJobsResponse:
		w.recordActiveJobsResponse(*msg)
		return nil, true
	case ipc.ActiveJobsResponse:
		w.recordActiveJobsResponse(msg)
		return nil, true
	case *ipc.ReloadJobsRequest, ipc.ReloadJobsRequest:
		resp := w.reloadJobsResponse()
		return &resp, true
	case *ipc.Reloaded, ipc.Reloaded:
		w.Reloaded()
		return nil, true
	default:
		return nil, false
	}
}

func (w *Watcher) handleReloadIPCMessage(r io.Reader, out io.Writer) (bool, error) {
	msg, err := ipc.ReadMessage(r)
	if err != nil {
		return false, err
	}
	payload, err := ipc.DecodePayload(msg)
	if err != nil {
		return false, err
	}

	resp, handled := w.handleReloadMessage(payload)
	if !handled || resp == nil {
		return handled, nil
	}

	responseMsg, err := ipc.NewMessage(resp)
	if err != nil {
		return true, err
	}
	if err := ipc.WriteMessage(out, responseMsg); err != nil {
		return true, err
	}
	return true, nil
}

func (w *Watcher) requestReloadJobs(out io.Writer) error {
	msg, err := ipc.NewMessage(&ipc.ReloadJobsRequest{})
	if err != nil {
		return err
	}
	return ipc.WriteMessage(out, msg)
}

func (w *Watcher) requestActiveJobs(out io.Writer) error {
	msg, err := ipc.NewMessage(&ipc.ActiveJobsRequest{})
	if err != nil {
		return err
	}
	return ipc.WriteMessage(out, msg)
}

func (w *Watcher) processReloadIPCMessages(r io.Reader, out io.Writer) error {
	for {
		_, err := w.handleReloadIPCMessage(r, out)
		if err == nil {
			continue
		}
		if errors.Is(err, io.EOF) {
			return nil
		}
		return err
	}
}

func (w *Watcher) runReloadIPCSession(rw io.ReadWriter) error {
	if err := w.requestReloadJobs(rw); err != nil {
		return err
	}
	return w.processReloadIPCMessages(rw, rw)
}

func (w *Watcher) triggerReload() bool {
	if w.onChange == nil {
		return false
	}
	if !w.markReloading() {
		return false
	}
	if w.reloadIPC != nil {
		waitCh := w.beginActiveJobsWait()
		if err := w.requestActiveJobs(w.reloadIPC); err != nil {
			logger.Logger.Warnw("failed to request active jobs before reload", err)
			w.clearActiveJobsWait(waitCh)
		} else {
			w.waitForActiveJobs(waitCh)
		}
	}
	w.incrementReloadCount()
	w.onChange()
	return true
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
						w.triggerReload()
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

	var w *Watcher
	w = NewWatcher([]string{"./"}, func() {
		logger.Logger.Infow("Triggering rebuild and restart")
		startCmd()
		w.Reloaded()
	})

	if err := w.Start(); err != nil {
		return err
	}
	defer w.Stop()

	startCmd()

	// Wait forever
	select {}
}
