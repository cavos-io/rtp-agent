#ifndef TEN_VAD_H
#define TEN_VAD_H

#include <stdint.h>
#include <stdbool.h>

#ifdef __cplusplus
extern "C" {
#endif

#if defined(_WIN32) || defined(_WIN64)
    #define TEN_VAD_EXPORT __declspec(dllexport)
    #define TEN_VAD_IMPORT __declspec(dllimport)
#else
    #define TEN_VAD_EXPORT __attribute__((visibility("default")))
    #define TEN_VAD_IMPORT
#endif

#ifdef TEN_VAD_BUILD
    #define TEN_VAD_API TEN_VAD_EXPORT
#else
    #define TEN_VAD_API TEN_VAD_IMPORT
#endif

typedef void* ten_vad_handle_t;

TEN_VAD_API ten_vad_handle_t ten_vad_create(int hop_size, float threshold);

TEN_VAD_API void ten_vad_process(ten_vad_handle_t handle, const int16_t* samples, int sample_count, float* probability, bool* speaking);

TEN_VAD_API void ten_vad_destroy(ten_vad_handle_t handle);

TEN_VAD_API const char* ten_vad_get_version();

#ifdef __cplusplus
}
#endif

#endif // TEN_VAD_H
