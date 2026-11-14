#include "bridge.h"
#include <dlfcn.h>
#include <time.h>
extern void goEvent(uint8_t*, size_t);
static void* library;
static void (*initialize)(void (*)(const uint8_t*,size_t),bool,const char*,const char*);
static uint64_t (*request)(const uint8_t*,size_t,const uint8_t**,size_t*);
static bool (*drop)(uint64_t);
static void (*dispose)(void);
static void callback(const uint8_t* p,size_t n) { goEvent((uint8_t*)p,n); }
const char* probe_load(const char* path) {
 // The artifact is process-scoped; do not increment dlopen references per room.
 if (library) return initialize && request && drop && dispose ? NULL : "missing required FFI symbols";
 library=dlopen(path,RTLD_NOW|RTLD_LOCAL);
 if(!library) return dlerror();
 initialize=dlsym(library,"livekit_ffi_initialize");
 request=dlsym(library,"livekit_ffi_request");
 drop=dlsym(library,"livekit_ffi_drop_handle");
 dispose=dlsym(library,"livekit_ffi_dispose");
 if(!initialize||!request||!drop||!dispose) return "missing required FFI symbols";
 return NULL;
}
void probe_init(void) { initialize(callback,false,"go-receive-probe","0.1.0"); }
uint64_t probe_request(const uint8_t* p,size_t n,const uint8_t** r,size_t* rn) {return request(p,n,r,rn);}
bool probe_drop(uint64_t h){return drop(h);}
void probe_dispose(void){dispose();} // Keep library loaded until process exit: no callback into unloaded code.
int64_t probe_now(void){struct timespec t;clock_gettime(CLOCK_MONOTONIC,&t);return (int64_t)t.tv_sec*1000000000+t.tv_nsec;}
void* probe_pointer(uint64_t p){return (void*)(uintptr_t)p;}
int probe_flush_packet_trace(void) {
 int (*flush)(void)=dlsym(RTLD_DEFAULT,"probe_packet_trace_flush");
 return flush ? flush() : -1;
}
