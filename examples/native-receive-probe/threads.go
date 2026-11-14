package main

/*
#cgo darwin LDFLAGS: -lproc
#include <stdint.h>
#include <stddef.h>
#include <string.h>
#ifdef __APPLE__
#include <mach/mach.h>
#include <pthread.h>
#include <libproc.h>
#include <unistd.h>
#endif

typedef struct { uint64_t id; char name[64]; } probe_native_thread;
static uint64_t callback_thread_id(void) {
#ifdef __APPLE__
 uint64_t id=0; pthread_threadid_np(NULL,&id); return id;
#else
 return 0;
#endif
}
static int native_threads(probe_native_thread* out, int capacity) {
#ifdef __APPLE__
 thread_act_array_t threads; mach_msg_type_number_t count=0;
 if (task_threads(mach_task_self(), &threads, &count)!=KERN_SUCCESS) return -1;
 int written=0;
 for (mach_msg_type_number_t i=0;i<count;i++) {
  thread_identifier_info_data_t info;
  mach_msg_type_number_t n=THREAD_IDENTIFIER_INFO_COUNT;
  if (written<capacity && thread_info(threads[i],THREAD_IDENTIFIER_INFO,(thread_info_t)&info,&n)==KERN_SUCCESS) {
   out[written].id=info.thread_id;
   struct proc_threadinfo details;
   memset(&details,0,sizeof(details));
   if (proc_pidinfo(getpid(),PROC_PIDTHREADINFO,info.thread_id,&details,sizeof(details))==sizeof(details)) {
    strncpy(out[written].name,details.pth_name,sizeof(out[written].name)-1);
   }
   written++;
  }
  mach_port_deallocate(mach_task_self(),threads[i]);
 }
 vm_deallocate(mach_task_self(),(vm_address_t)threads,count*sizeof(thread_t));
 return count>(mach_msg_type_number_t)capacity ? -2 : written;
#else
 return -1;
#endif
}
*/
import "C"
import (
	"fmt"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
)

// Diagnostic-only, disabled for the original receive command. No audio policy.
var inspectCallbacks atomic.Bool
var callbackThreadIDs sync.Map

func captureCallbackThread() {
	var stack [128]byte
	n := runtime.Stack(stack[:], false)
	fields := strings.Fields(string(stack[:n]))
	if len(fields) >= 2 {
		callbackThreadIDs.LoadOrStore(fields[1], uint64(C.callback_thread_id()))
	}
}

type nativeThread struct {
	ID   uint64 `json:"id"`
	Name string `json:"name"`
}

func nativeThreads() ([]nativeThread, error) {
	var buffer [512]C.probe_native_thread
	n := int(C.native_threads(&buffer[0], C.int(len(buffer))))
	if n < 0 {
		return nil, fmt.Errorf("native thread snapshot failed: %d", n)
	}
	result := make([]nativeThread, 0, n)
	for i := 0; i < n; i++ {
		result = append(result, nativeThread{uint64(buffer[i].id), C.GoString(&buffer[i].name[0])})
	}
	return result, nil
}
func callbackThreadSnapshot() map[string]uint64 {
	result := map[string]uint64{}
	callbackThreadIDs.Range(func(k, v any) bool { result[k.(string)] = v.(uint64); return true })
	return result
}
