#include <stdint.h>
#include <stddef.h>
#include <stdbool.h>
const char* nip_load(const char* path);
void nip_init(void);
uint64_t nip_request(const uint8_t*, size_t, const uint8_t**, size_t*);
bool nip_drop(uint64_t);
void nip_dispose(void);
int64_t nip_now(void);
void* nip_pointer(uint64_t);
