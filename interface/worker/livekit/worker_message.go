package livekit

import (
	"sort"

	"github.com/gorilla/websocket"
	lkprotocol "github.com/livekit/protocol/livekit"
)

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
