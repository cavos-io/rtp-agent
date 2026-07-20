//go:build ffmpeg && cgo

#include "recorder.h"

#include <libavcodec/avcodec.h>
#include <libavformat/avformat.h>
#include <libavutil/channel_layout.h>
#include <libavutil/error.h>
#include <libavutil/log.h>
#include <libavutil/opt.h>
#include <libswresample/swresample.h>
#include <stdlib.h>

struct rtp_mp4_writer {
	AVFormatContext *format;
	AVCodecContext *codec;
	AVStream *stream;
	SwrContext *resampler;
	AVFrame *frame;
	int64_t next_pts;
};

void rtp_mp4_configure_logging(void) {
	av_log_set_level(AV_LOG_WARNING);
}

static void rtp_mp4_free(rtp_mp4_writer *writer) {
	if (!writer) return;
	av_frame_free(&writer->frame);
	swr_free(&writer->resampler);
	avcodec_free_context(&writer->codec);
	if (writer->format) {
		if (!(writer->format->oformat->flags & AVFMT_NOFILE)) {
			avio_closep(&writer->format->pb);
		}
		avformat_free_context(writer->format);
	}
	free(writer);
}

static int rtp_mp4_mux_packets(rtp_mp4_writer *writer) {
	AVPacket *packet = av_packet_alloc();
	if (!packet) return AVERROR(ENOMEM);

	int result = 0;
	while ((result = avcodec_receive_packet(writer->codec, packet)) >= 0) {
		av_packet_rescale_ts(packet, writer->codec->time_base, writer->stream->time_base);
		packet->stream_index = writer->stream->index;
		result = av_interleaved_write_frame(writer->format, packet);
		av_packet_unref(packet);
		if (result < 0) break;
	}
	av_packet_free(&packet);
	return result == AVERROR(EAGAIN) || result == AVERROR_EOF ? 0 : result;
}

int rtp_mp4_open(const char *path, int sample_rate, rtp_mp4_writer **out) {
	int result;
	rtp_mp4_writer *writer = calloc(1, sizeof(*writer));
	if (!writer) return AVERROR(ENOMEM);

	result = avformat_alloc_output_context2(&writer->format, NULL, "mp4", path);
	if (result < 0 || !writer->format) goto fail;

	const AVCodec *encoder = avcodec_find_encoder(AV_CODEC_ID_AAC);
	if (!encoder) {
		result = AVERROR_ENCODER_NOT_FOUND;
		goto fail;
	}
	writer->stream = avformat_new_stream(writer->format, NULL);
	if (!writer->stream) {
		result = AVERROR(ENOMEM);
		goto fail;
	}
	writer->codec = avcodec_alloc_context3(encoder);
	if (!writer->codec) {
		result = AVERROR(ENOMEM);
		goto fail;
	}

	writer->codec->sample_fmt = AV_SAMPLE_FMT_FLTP;
	writer->codec->sample_rate = sample_rate;
	writer->codec->bit_rate = 128000;
	writer->codec->time_base = (AVRational){1, sample_rate};
	AVChannelLayout stereo = AV_CHANNEL_LAYOUT_STEREO;
	if ((result = av_channel_layout_copy(&writer->codec->ch_layout, &stereo)) < 0) goto fail;
	if (writer->format->oformat->flags & AVFMT_GLOBALHEADER) {
		writer->codec->flags |= AV_CODEC_FLAG_GLOBAL_HEADER;
	}
	if ((result = avcodec_open2(writer->codec, encoder, NULL)) < 0) goto fail;
	if ((result = avcodec_parameters_from_context(writer->stream->codecpar, writer->codec)) < 0) goto fail;
	writer->stream->time_base = writer->codec->time_base;

	AVChannelLayout input_layout = AV_CHANNEL_LAYOUT_STEREO;
	result = swr_alloc_set_opts2(
		&writer->resampler,
		&writer->codec->ch_layout,
		writer->codec->sample_fmt,
		writer->codec->sample_rate,
		&input_layout,
		AV_SAMPLE_FMT_S16,
		sample_rate,
		0,
		NULL
	);
	if (result < 0) goto fail;
	if ((result = swr_init(writer->resampler)) < 0) goto fail;

	writer->frame = av_frame_alloc();
	if (!writer->frame) {
		result = AVERROR(ENOMEM);
		goto fail;
	}
	writer->frame->format = writer->codec->sample_fmt;
	writer->frame->sample_rate = writer->codec->sample_rate;
	writer->frame->nb_samples = writer->codec->frame_size;
	if ((result = av_channel_layout_copy(&writer->frame->ch_layout, &writer->codec->ch_layout)) < 0) goto fail;
	if ((result = av_frame_get_buffer(writer->frame, 0)) < 0) goto fail;

	if (!(writer->format->oformat->flags & AVFMT_NOFILE)) {
		if ((result = avio_open(&writer->format->pb, path, AVIO_FLAG_WRITE)) < 0) goto fail;
	}
	AVDictionary *options = NULL;
	av_dict_set(&options, "movflags", "+faststart", 0);
	result = avformat_write_header(writer->format, &options);
	av_dict_free(&options);
	if (result < 0) goto fail;

	*out = writer;
	return writer->codec->frame_size;

fail:
	rtp_mp4_free(writer);
	return result < 0 ? result : AVERROR_UNKNOWN;
}

int rtp_mp4_write(rtp_mp4_writer *writer, const int16_t *samples, int samples_per_channel) {
	int result = av_frame_make_writable(writer->frame);
	if (result < 0) return result;

	const uint8_t *input[1] = {(const uint8_t *)samples};
	result = swr_convert(
		writer->resampler,
		writer->frame->data,
		writer->frame->nb_samples,
		input,
		samples_per_channel
	);
	if (result < 0) return result;

	writer->frame->nb_samples = result;
	writer->frame->pts = writer->next_pts;
	writer->next_pts += result;
	if ((result = avcodec_send_frame(writer->codec, writer->frame)) < 0) return result;
	return rtp_mp4_mux_packets(writer);
}

int rtp_mp4_close(rtp_mp4_writer *writer) {
	int result = avcodec_send_frame(writer->codec, NULL);
	if (result >= 0) result = rtp_mp4_mux_packets(writer);
	if (result >= 0) result = av_write_trailer(writer->format);
	rtp_mp4_free(writer);
	return result;
}

void rtp_mp4_error_string(int error_code, char *buffer, size_t buffer_size) {
	av_strerror(error_code, buffer, buffer_size);
}
