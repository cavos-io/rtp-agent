#!/usr/bin/env python3
"""Execute the pinned AudioRecognition lifecycle using its test doubles."""
import asyncio
import json
from pathlib import Path
import runpy
import sys
from unittest.mock import MagicMock

root = Path(__file__).resolve().parents[2]
sys.path.insert(0, str(root / 'refs/agents/livekit-agents'))
reference = runpy.run_path(str(root / 'refs/agents/tests/test_audio_recognition_turn_detection.py'))

async def run(scenario):
    ar = reference['_make_full_recognition_for_eou']()
    ar._stt_consumer_atask = None
    ar._end_user_turn_span = MagicMock()  # exclude tracing, retain lifecycle behavior
    events = []
    for event in scenario['events']:
        if event['type'] == 'clear':
            ar._clear_user_turn()
        elif event['type'] == 'start_of_speech':
            await ar._on_vad_event(reference['_start_of_speech']())
        else:
            await ar._on_vad_event(reference['_inference_done'](raw_speech=event.get('speech', 0), raw_silence=event.get('silence', 0)))
        events.append(dict(type=event['type'], pending=ar._turn_detector_prediction_fut is not None, predictions=ar._turn_detector_stream.predict.call_count))
    return dict(contract=scenario['contract'], events=events)

print(json.dumps(asyncio.run(run(json.loads(Path(sys.argv[1]).read_text())))))
