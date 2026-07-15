#ifndef RTP_AGENT_FFMPEG_RECORDER_H
#define RTP_AGENT_FFMPEG_RECORDER_H

#include <stddef.h>
#include <stdint.h>

#define RTP_MP4_ERROR_BUFFER_SIZE 128

typedef struct rtp_mp4_writer rtp_mp4_writer;

void rtp_mp4_configure_logging(void);
int rtp_mp4_open(const char *path, int sample_rate, rtp_mp4_writer **out);
int rtp_mp4_write(rtp_mp4_writer *writer, const int16_t *samples, int samples_per_channel);
int rtp_mp4_close(rtp_mp4_writer *writer);
void rtp_mp4_error_string(int error_code, char *buffer, size_t buffer_size);

#endif
