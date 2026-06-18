package livekit

import (
	"context"
	"fmt"
	"sort"

	"github.com/gorilla/websocket"
	lkprotocol "github.com/livekit/protocol/livekit"
)

type WorkerMessage = lkprotocol.WorkerMessage

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

type EntrypointResult struct {
	Status    lkprotocol.JobStatus
	Err       error
	Recovered any
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
