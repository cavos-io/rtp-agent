#ifndef OPUS_H
#define OPUS_H

typedef int OpusEncoder;
typedef int OpusDecoder;
typedef int opus_int16;
typedef int opus_int32;

#define OPUS_OK 0
#define OPUS_BAD_ARG -1
#define OPUS_BUFFER_TOO_SMALL -2
#define OPUS_INTERNAL_ERROR -3
#define OPUS_INVALID_PACKET -4
#define OPUS_UNIMPLEMENTED -5
#define OPUS_INVALID_STATE -6
#define OPUS_ALLOC_FAIL -7

#define OPUS_APPLICATION_VOIP 2048
#define OPUS_APPLICATION_AUDIO 2049
#define OPUS_APPLICATION_RESTRICTED_LOWDELAY 2051

inline const char* opus_get_version_string(void) { return "stub"; }
inline const char* opus_strerror(int error) { return "stub error"; }
inline int opus_decoder_get_size(int channels) { return 0; }
inline int opus_decoder_init(OpusDecoder* st, opus_int32 fs, int channels) { return 0; }
inline int opus_decode(OpusDecoder* st, const unsigned char* data, opus_int32 len, opus_int16* pcm, int frame_size, int decode_fec) { return 0; }
inline int opus_decode_float(OpusDecoder* st, const unsigned char* data, opus_int32 len, float* pcm, int frame_size, int decode_fec) { return 0; }

// Minimal opusfile stubs
typedef struct OggOpusFile OggOpusFile;
#define OP_FALSE         -1
#define OP_EOF           -2
#define OP_HOLE          -3
#define OP_EREAD         -11
#define OP_EFAULT        -12
#define OP_EIMPL         -13
#define OP_EINVAL        -14
#define PaStream         int
#define PaError          int
#define paNoError        0

#endif