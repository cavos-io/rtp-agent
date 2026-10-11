#!/usr/bin/env python3
"""Compare actual pinned AudioRecognition span lifecycle, without model inference."""
import json
from pathlib import Path
import sys
import time

root = Path(__file__).resolve().parents[2]
sys.path.insert(0, str(root / 'refs/agents/livekit-agents'))
from opentelemetry import trace
from opentelemetry.sdk.trace import TracerProvider
from opentelemetry.sdk.trace.export import SimpleSpanProcessor
from opentelemetry.sdk.trace.export.in_memory_span_exporter import InMemorySpanExporter
from livekit.agents.telemetry import set_tracer_provider, tracer
from livekit.agents.voice.audio_recognition import AudioRecognition

scenario = json.loads(Path(sys.argv[1]).read_text())
exporter = InMemorySpanExporter()
provider = TracerProvider()
provider.add_span_processor(SimpleSpanProcessor(exporter))
set_tracer_provider(provider)
ar = AudioRecognition.__new__(AudioRecognition)
ar._eou_wait_span = None
ar._eou_wait_started_at_ns = None
ar._eou_wait_floor_ns = None
ar._eou_wait_rearms = ar._eou_wait_not_committed = ar._user_turn_resumes = 0
ar._eou_detection_span = None
user = tracer.start_span('user_turn', start_time=time.time_ns()-2_000_000_000)
tracking = None
for event in scenario['events']:
    kind = event['type']
    if kind == 'begin':
        ar._ensure_eou_wait_span(user, trigger=event['source'], last_speaking_time=time.time()-1, endpointing_delay=.3)
    elif kind == 'detect':
        detection = tracer.start_span('eou_detection', context=trace.set_span_in_context(ar._eou_wait_span))
        tracking = ar._track_eou_detection_span(detection)
        tracking.__enter__()
    elif kind == 'detected':
        tracking.__exit__(None, None, None)
        tracking = None
    elif kind == 'not_committed':
        ar._eou_wait_not_committed += 1
    elif kind == 'finish':
        ar._end_eou_wait_span(event['outcome'], end_time=time.time()-.1 if event['outcome']=='user_resumed' else None)
        if tracking:
            tracking.__exit__(None, None, None)
            tracking = None
ar._stamp_user_turn_resumes(user)
user.end()
spans = exporter.get_finished_spans()
by_id = {s.context.span_id:s for s in spans}
normalized = []
keys = ['lk.eou.source','lk.eou.outcome','lk.eou.endpointing_delay','lk.eou.rearm_count','lk.eou.not_committed_count','lk.eou.resume_count']
for s in spans:
    parent = by_id.get(s.parent.span_id) if s.parent else None
    attrs = {k:s.attributes[k] for k in keys if k in s.attributes}
    contained = parent is None or parent.start_time <= s.start_time <= s.end_time <= parent.end_time
    duration_matches = s.name != 'eou_wait' or abs(s.attributes['lk.eou.wait_duration']-(s.end_time-s.start_time)/1e9)<1e-9
    normalized.append(dict(name=s.name, parent=parent.name if parent else '', attributes=attrs, events=[e.name for e in s.events], contained=contained, duration_matches=duration_matches))
print(json.dumps(dict(contract=scenario['contract'], events=normalized)))
provider.shutdown()
