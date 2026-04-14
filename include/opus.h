#ifndef OPUS_STUB_H
#define OPUS_STUB_H

// Basic types
typedef struct OpusEncoder OpusEncoder;
typedef struct OpusDecoder OpusDecoder;
typedef short opus_int16;
typedef int opus_int32;
typedef struct OggOpusFile OggOpusFile;

// Structures to satisfy Go pointers
struct OpusEncoder { int dummy; };
struct OpusDecoder { int dummy; };

// OpusFileCallbacks struct
struct OpusFileCallbacks {
  int (*read)(void *_datasource, unsigned char *_ptr, int _nbytes);
  int (*seek)(void *_datasource, long _offset, int _whence);
  int (*tell)(void *_datasource);
  int (*close)(void *_datasource);
};

// Use enums for CGO to better detect constants
enum {
    OPUS_OK = 0,
    OPUS_BAD_ARG = -1,
    OPUS_BUFFER_TOO_SMALL = -2,
    OPUS_INTERNAL_ERROR = -3,
    OPUS_INVALID_PACKET = -4,
    OPUS_UNIMPLEMENTED = -5,
    OPUS_INVALID_STATE = -6,
    OPUS_ALLOC_FAIL = -7,
    
    OPUS_APPLICATION_VOIP = 2048,
    OPUS_APPLICATION_AUDIO = 2049,
    OPUS_APPLICATION_RESTRICTED_LOWDELAY = 2051,
    
    OPUS_AUTO = -1000,
    OPUS_BITRATE_MAX = -1,
    
    OPUS_BANDWIDTH_NARROWBAND = 1101,
    OPUS_BANDWIDTH_MEDIUMBAND = 1102,
    OPUS_BANDWIDTH_WIDEBAND = 1103,
    OPUS_BANDWIDTH_SUPERWIDEBAND = 1104,
    OPUS_BANDWIDTH_FULLBAND = 1105,

    OPUS_RESET_STATE = 4028,
    
    OP_FALSE = -1,
    OP_EOF = -2,
    OP_HOLE = -3,
    OP_EREAD = -11,
    OP_EFAULT = -12,
    OP_EIMPL = -13,
    OP_EINVAL = -14,
    OP_ENOTFORMAT = -132,
    OP_EBADHEADER = -133,
    OP_EVERSION = -134,
    OP_ENOTAUDIO = -135,
    OP_EBADPACKET = -136,
    OP_EBADLINK = -137,
    OP_ENOSEEK = -138,
    OP_EBADTIMESTAMP = -139
};

// Macro stubs as functions for CGO
static int OPUS_SET_BITRATE(int x) { return 0; }
static int OPUS_GET_BITRATE(int* x) { return 0; }
static int OPUS_SET_COMPLEXITY(int x) { return 0; }
static int OPUS_GET_COMPLEXITY(int* x) { return 0; }
static int OPUS_SET_MAX_BANDWIDTH(int x) { return 0; }
static int OPUS_GET_MAX_BANDWIDTH(int* x) { return 0; }
static int OPUS_SET_INBAND_FEC(int x) { return 0; }
static int OPUS_GET_INBAND_FEC(int* x) { return 0; }
static int OPUS_SET_PACKET_LOSS_PERC(int x) { return 0; }
static int OPUS_GET_PACKET_LOSS_PERC(int* x) { return 0; }
static int OPUS_SET_DTX(int x) { return 0; }
static int OPUS_GET_DTX(int* x) { return 0; }
static int OPUS_GET_IN_DTX(int* x) { return 0; }
static int OPUS_GET_SAMPLE_RATE(int* x) { return 0; }
static int OPUS_GET_LAST_PACKET_DURATION(int* x) { return 0; }

// Function Stubs
static const char* opus_get_version_string(void) { return "stub"; }
static const char* opus_strerror(int error) { return "stub error"; }

static int opus_decoder_get_size(int channels) { return 0; }
static int opus_decoder_init(OpusDecoder* st, opus_int32 fs, int channels) { return 0; }
static int opus_decode(OpusDecoder* st, const unsigned char* data, opus_int32 len, opus_int16* pcm, int frame_size, int decode_fec) { return 0; }
static int opus_decode_float(OpusDecoder* st, const unsigned char* data, opus_int32 len, float* pcm, int frame_size, int decode_fec) { return 0; }

static int opus_encoder_get_size(int channels) { return 0; }
static int opus_encoder_init(OpusEncoder* st, opus_int32 fs, int channels, int application) { return 0; }
static int opus_encode(OpusEncoder* st, const opus_int16* pcm, int frame_size, unsigned char* data, opus_int32 max_data_bytes) { return 0; }
static int opus_encode_float(OpusEncoder* st, const float* pcm, int frame_size, unsigned char* data, opus_int32 max_data_bytes) { return 0; }
static int opus_encoder_ctl(OpusEncoder* st, int request, ...) { return 0; }
static int opus_decoder_ctl(OpusDecoder* st, int request, ...) { return 0; }

static void op_free(OggOpusFile* _of) {}
static int op_read(OggOpusFile* _of, opus_int16* _pcm, int _buf_size, int* _li) { return 0; }
static int op_read_float(OggOpusFile* _of, float* _pcm, int _buf_size, int* _li) { return 0; }
static OggOpusFile* op_open_callbacks(void* p, struct OpusFileCallbacks* cb, const unsigned char* initial_data, int initial_bytes, int* error) { return 0; }

// PortAudio Stubs
#define PaStream         int
#define PaError          int
#define paNoError        0

#endif