// Package nativeroom provides experimental opt-in LiveKit session transports.
//
// Build with nativeinprocess to use NewInProcess. Pass the returned transport to
// worker.StartSessionOptions.Transport before connecting the job's Pion room.
// The caller must explicitly disable unsupported pre-connect audio in RoomOptions.
// Native owns receive playout, reconnect, and both media directions.
//
// One FFI runtime is shared by all native sessions in the process. Close session
// transports first, then call CloseInProcessRuntime once during worker shutdown.
// Runtime disposal is terminal; the native library cannot be swapped or reopened.
// Construction does not select a transport globally or load the native library.
//
// New retains the existing helper-process transport. Neither constructor changes
// the worker's default transport. See docs/features/native-public-api.md for
// deployment, downstream migration and compatibility limits.
package nativeroom
