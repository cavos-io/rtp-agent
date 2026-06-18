package livekit

import (
	"context"
	"fmt"
	"sort"
	"time"

	"github.com/gorilla/websocket"
	lkprotocol "github.com/livekit/protocol/livekit"
)

type WorkerMessage = lkprotocol.WorkerMessage

type JobStatus = lkprotocol.JobStatus

type WorkerStatusUpdateOptions struct {
	Draining     bool
	Load         float64
	JobCount     uint32
	CanAcceptJob bool
}

type WorkerWebSocketReader interface {
	ReadMessage() (int, []byte, error)
}

type WorkerWebSocketReadFunc func() (int, []byte, error)

func (fn WorkerWebSocketReadFunc) ReadMessage() (int, []byte, error) {
	return fn()
}

type WorkerMessageLoopOptions struct {
	Reader        WorkerWebSocketReader
	Close         func() error
	Handle        func(*lkprotocol.ServerMessage)
	OnDecodeError func(error)
}

func WorkerStatusMessage(status lkprotocol.WorkerStatus, load float64, jobCount uint32) *lkprotocol.WorkerMessage {
	return &lkprotocol.WorkerMessage{
		Message: &lkprotocol.WorkerMessage_UpdateWorker{
			UpdateWorker: &lkprotocol.UpdateWorkerStatus{
				Status:   &status,
				Load:     float32(load),
				JobCount: jobCount,
			},
		},
	}
}

func WorkerMessageWebSocketFrame(msg *lkprotocol.WorkerMessage) (int, []byte, error) {
	binary, data, err := WorkerMessageFrame(msg)
	if err != nil {
		return 0, nil, err
	}
	msgType := websocket.TextMessage
	if binary {
		msgType = websocket.BinaryMessage
	}
	return msgType, data, nil
}

type WorkerMessageWebSocketWriter interface {
	WriteMessage(int, []byte) error
}

type WorkerRegisterWebSocket interface {
	WorkerMessageWebSocketWriter
	ReadMessage() (int, []byte, error)
}

func WriteWorkerMessageWebSocket(writer WorkerMessageWebSocketWriter, msg *lkprotocol.WorkerMessage) error {
	msgType, data, err := WorkerMessageWebSocketFrame(msg)
	if err != nil {
		return err
	}
	return writer.WriteMessage(msgType, data)
}

func ExchangeInitialRegisterWebSocket(conn WorkerRegisterWebSocket, msg *lkprotocol.WorkerMessage) (*lkprotocol.ServerMessage, error) {
	if err := WriteWorkerMessageWebSocket(conn, msg); err != nil {
		return nil, err
	}
	msgType, data, err := conn.ReadMessage()
	if err != nil {
		return nil, err
	}
	return InitialRegisterWebSocketMessage(msgType, data)
}

func RunWorkerMessageLoop(ctx context.Context, opts WorkerMessageLoopOptions) error {
	if opts.Reader == nil {
		return fmt.Errorf("worker websocket reader is required")
	}

	for {
		readDone := make(chan struct {
			msgType int
			data    []byte
			err     error
		}, 1)
		go func() {
			msgType, data, err := opts.Reader.ReadMessage()
			readDone <- struct {
				msgType int
				data    []byte
				err     error
			}{msgType: msgType, data: data, err: err}
		}()

		select {
		case <-ctx.Done():
			if opts.Close != nil {
				_ = opts.Close()
			}
			return ctx.Err()
		case result := <-readDone:
			if result.err != nil {
				return result.err
			}

			msg, err := ServerMessageWebSocketFrame(result.msgType, result.data)
			if err != nil {
				if opts.OnDecodeError != nil {
					opts.OnDecodeError(err)
				}
				continue
			}
			if msg == nil {
				continue
			}
			if opts.Handle != nil {
				opts.Handle(msg)
			}
		}
	}
}

func AvailableWorkerStatusMessage(load float64, jobCount uint32, canAcceptJob bool) *lkprotocol.WorkerMessage {
	status := lkprotocol.WorkerStatus_WS_AVAILABLE
	if !canAcceptJob {
		status = lkprotocol.WorkerStatus_WS_FULL
	}
	return WorkerStatusMessage(status, load, jobCount)
}

func WorkerStatusUpdateMessage(opts WorkerStatusUpdateOptions) *lkprotocol.WorkerMessage {
	if opts.Draining {
		return DrainingWorkerStatusMessage(opts.JobCount)
	}
	return AvailableWorkerStatusMessage(opts.Load, opts.JobCount, opts.CanAcceptJob)
}

func DrainingWorkerStatusMessage(jobCount uint32) *lkprotocol.WorkerMessage {
	status := lkprotocol.WorkerStatus_WS_FULL
	return &lkprotocol.WorkerMessage{
		Message: &lkprotocol.WorkerMessage_UpdateWorker{
			UpdateWorker: &lkprotocol.UpdateWorkerStatus{
				Status:   &status,
				JobCount: jobCount,
			},
		},
	}
}

func JobStatusForEntrypointResult(err error, recovered any) lkprotocol.JobStatus {
	if err != nil || recovered != nil {
		return lkprotocol.JobStatus_JS_FAILED
	}
	return lkprotocol.JobStatus_JS_SUCCESS
}

func JobStatusSucceeded(status lkprotocol.JobStatus) bool {
	return status == lkprotocol.JobStatus_JS_SUCCESS
}

type JobCompletionPlan struct {
	Finish          bool
	WaitForShutdown bool
	SendStatus      bool
	SendAfterFinish bool
}

func JobCompletionPlanForEntrypoint(status lkprotocol.JobStatus, terminated bool) JobCompletionPlan {
	if terminated {
		return JobCompletionPlan{Finish: true}
	}
	if JobStatusSucceeded(status) {
		return JobCompletionPlan{
			Finish:          true,
			WaitForShutdown: true,
			SendStatus:      true,
			SendAfterFinish: true,
		}
	}
	return JobCompletionPlan{
		Finish:     true,
		SendStatus: true,
	}
}

type EntrypointResult struct {
	Status    lkprotocol.JobStatus
	Err       error
	Recovered any
}

type JobEntrypointLifecycleOptions struct {
	Context      context.Context
	Entrypoint   func() error
	MarkDone     func()
	OnResult     func(EntrypointResult)
	Terminated   func() bool
	ShutdownDone <-chan struct{}
	Shutdown     func(string)
	Finish       func() bool
	SendStatus   func(lkprotocol.JobStatus) error
}

type ReloadedJobEntrypointLifecycleOptions struct {
	Context         context.Context
	Entrypoint      func() error
	MarkDone        func()
	OnResult        func(EntrypointResult)
	ShutdownDone    <-chan struct{}
	Shutdown        func(string)
	Finish          func() bool
	SendStatus      func(lkprotocol.JobStatus) error
	OnStatusSkipped func()
}

type RunningJobEntrypointLifecycleOptions struct {
	Context            context.Context
	Entrypoint         func() error
	MarkStarted        func()
	MarkDone           func()
	ShutdownDone       <-chan struct{}
	Shutdown           func(string)
	WaitEntrypointDone func(time.Duration) bool
	CloseWait          time.Duration
	Finish             func() bool
	OnPanic            func(any)
	OnError            func(error)
	OnCancelTimeout    func()
}

func RunEntrypoint(entrypoint func() error) (result EntrypointResult) {
	result.Status = JobStatusForEntrypointResult(nil, nil)
	defer func() {
		if recovered := recover(); recovered != nil {
			result.Recovered = recovered
			result.Status = JobStatusForEntrypointResult(nil, recovered)
		}
	}()
	if err := entrypoint(); err != nil {
		result.Err = err
		result.Status = JobStatusForEntrypointResult(err, nil)
	}
	return result
}

func RunRunningJobEntrypointLifecycle(opts RunningJobEntrypointLifecycleOptions) error {
	ctx := opts.Context
	if ctx == nil {
		ctx = context.Background()
	}
	doneCh := make(chan error, 1)
	if opts.MarkStarted != nil {
		opts.MarkStarted()
	}
	go func() {
		defer func() {
			if opts.MarkDone != nil {
				opts.MarkDone()
			}
		}()
		defer func() {
			if recovered := recover(); recovered != nil {
				if opts.OnPanic != nil {
					opts.OnPanic(recovered)
				}
				doneCh <- fmt.Errorf("running job entrypoint panicked: %v", recovered)
			}
		}()
		doneCh <- runJobEntrypointFunc(opts.Entrypoint)
	}()

	select {
	case err := <-doneCh:
		if err != nil {
			if opts.OnError != nil {
				opts.OnError(err)
			}
			finishJobEntrypoint(opts.Finish)
			return err
		}
		select {
		case <-opts.ShutdownDone:
		case <-ctx.Done():
			if opts.Shutdown != nil {
				opts.Shutdown("")
			}
			finishJobEntrypoint(opts.Finish)
			return ctx.Err()
		}
		finishJobEntrypoint(opts.Finish)
		return nil
	case <-ctx.Done():
		if opts.Shutdown != nil {
			opts.Shutdown("")
		}
		if opts.WaitEntrypointDone != nil && !opts.WaitEntrypointDone(opts.CloseWait) && opts.OnCancelTimeout != nil {
			opts.OnCancelTimeout()
		}
		finishJobEntrypoint(opts.Finish)
		return ctx.Err()
	}
}

func runJobEntrypointFunc(entrypoint func() error) error {
	if entrypoint == nil {
		return nil
	}
	return entrypoint()
}

func RunReloadedJobEntrypointLifecycle(opts ReloadedJobEntrypointLifecycleOptions) EntrypointResult {
	result := RunEntrypoint(opts.Entrypoint)
	if opts.MarkDone != nil {
		opts.MarkDone()
	}
	if opts.OnResult != nil {
		opts.OnResult(result)
	}

	if JobStatusSucceeded(result.Status) {
		waitForReloadedJobShutdown(opts)
		if !finishJobEntrypoint(opts.Finish) {
			return result
		}
	} else {
		finishJobEntrypoint(opts.Finish)
	}

	ctx := opts.Context
	if ctx == nil {
		ctx = context.Background()
	}
	select {
	case <-ctx.Done():
		if opts.OnStatusSkipped != nil {
			opts.OnStatusSkipped()
		}
	default:
		_ = sendJobEntrypointStatus(opts.SendStatus, result.Status)
	}
	finishJobEntrypoint(opts.Finish)
	return result
}

func RunJobEntrypointLifecycle(opts JobEntrypointLifecycleOptions) EntrypointResult {
	result := RunEntrypoint(opts.Entrypoint)
	if opts.MarkDone != nil {
		opts.MarkDone()
	}
	if opts.OnResult != nil {
		opts.OnResult(result)
	}

	terminated := false
	if opts.Terminated != nil {
		terminated = opts.Terminated()
	}
	plan := JobCompletionPlanForEntrypoint(result.Status, terminated)
	if plan.WaitForShutdown {
		waitForJobShutdown(opts)
	}
	if plan.Finish && plan.SendAfterFinish {
		if !finishJobEntrypoint(opts.Finish) {
			return result
		}
	}
	if plan.SendStatus {
		_ = sendJobEntrypointStatus(opts.SendStatus, result.Status)
	}
	if plan.Finish && !plan.SendAfterFinish {
		finishJobEntrypoint(opts.Finish)
	}
	return result
}

func waitForReloadedJobShutdown(opts ReloadedJobEntrypointLifecycleOptions) {
	if opts.ShutdownDone == nil {
		return
	}
	ctx := opts.Context
	if ctx == nil {
		ctx = context.Background()
	}
	select {
	case <-opts.ShutdownDone:
	case <-ctx.Done():
		if opts.Shutdown != nil {
			opts.Shutdown("")
		}
	}
}

func waitForJobShutdown(opts JobEntrypointLifecycleOptions) {
	if opts.ShutdownDone == nil {
		return
	}
	ctx := opts.Context
	if ctx == nil {
		ctx = context.Background()
	}
	select {
	case <-opts.ShutdownDone:
	case <-ctx.Done():
		if opts.Shutdown != nil {
			opts.Shutdown("")
		}
	}
}

func finishJobEntrypoint(finish func() bool) bool {
	if finish == nil {
		return true
	}
	return finish()
}

func sendJobEntrypointStatus(sendStatus func(lkprotocol.JobStatus) error, status lkprotocol.JobStatus) error {
	if sendStatus == nil {
		return nil
	}
	return sendStatus(status)
}

func JobRunningMessage(jobID string) *lkprotocol.WorkerMessage {
	return JobStatusMessage(jobID, lkprotocol.JobStatus_JS_RUNNING)
}

func JobStatusMessage(jobID string, status lkprotocol.JobStatus) *lkprotocol.WorkerMessage {
	return &lkprotocol.WorkerMessage{
		Message: &lkprotocol.WorkerMessage_UpdateJob{
			UpdateJob: &lkprotocol.UpdateJobStatus{
				JobId:  jobID,
				Status: status,
			},
		},
	}
}

func MigrateJobMessage(jobIDs []string) *lkprotocol.WorkerMessage {
	sortedJobIDs := append([]string(nil), jobIDs...)
	sort.Strings(sortedJobIDs)
	return &lkprotocol.WorkerMessage{
		Message: &lkprotocol.WorkerMessage_MigrateJob{
			MigrateJob: &lkprotocol.MigrateJobRequest{JobIds: sortedJobIDs},
		},
	}
}

func MigratableRunningJobIDs(jobs []RunningJobInfo) []string {
	jobIDs := make([]string, 0, len(jobs))
	for _, job := range jobs {
		if job.FakeJob {
			continue
		}
		jobID := JobID(job.Job)
		if jobID == "" {
			continue
		}
		jobIDs = append(jobIDs, jobID)
	}
	sort.Strings(jobIDs)
	return jobIDs
}

func MigrateRunningJobsMessage(jobs []RunningJobInfo) *lkprotocol.WorkerMessage {
	return MigrateJobMessage(MigratableRunningJobIDs(jobs))
}
