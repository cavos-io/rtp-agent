#include <stdint.h>
#include <stddef.h>
#include <stdbool.h>
const char* probe_load(const char* path);
void probe_init(void);
uint64_t probe_request(const uint8_t*, size_t, const uint8_t**, size_t*);
bool probe_drop(uint64_t);
void probe_dispose(void);
int64_t probe_now(void);
void* probe_pointer(uint64_t);
