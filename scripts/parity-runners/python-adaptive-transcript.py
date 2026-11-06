#!/usr/bin/env python3
"""Execute pinned AudioRecognition gate methods; replace only external collaborators."""
import ast
from collections import deque
import json
from pathlib import Path
import subprocess
import sys
from types import SimpleNamespace
from unittest.mock import MagicMock

root = Path(__file__).resolve().parents[2]
revision = '31c6026df14eebb71975c16ca6a422ce68644b71'
source = subprocess.check_output([
    'git', '-C', str(root / 'refs/agents'), 'show',
    revision + ':livekit-agents/livekit/agents/voice/audio_recognition.py',
], text=True)
# Compile the original method ASTs unchanged. No reimplementation of the gate.
names = {'_trim_held_transcripts', '_flush_held_transcripts',
         '_on_start_of_agent_speech', '_on_start_of_overlap_speech',
         '_adaptive_interruption_active', '_backchannel_boundary_active',
         '_cancel_backchannel_boundary', '_on_overlap_speech_event'}
original = next(n for n in ast.parse(source).body if isinstance(n, ast.ClassDef) and n.name == 'AudioRecognition')
methods = [n for n in original.body if isinstance(n, (ast.FunctionDef, ast.AsyncFunctionDef)) and n.name in names]
assert len(methods) == len(names)
module = ast.Module(body=[ast.ImportFrom(module='__future__', names=[ast.alias(name='annotations')], level=0),
                         ast.ClassDef(name='Gate', bases=[], keywords=[], body=methods, decorator_list=[])], type_ignores=[])
namespace = dict(logger=MagicMock(), _UserTurnTracker=SimpleNamespace,
                 _AgentSpeechStartedSentinel=SimpleNamespace, _OverlapSpeechStartedSentinel=SimpleNamespace)
exec(compile(ast.fix_missing_locations(module), '<pinned AudioRecognition>', 'exec'), namespace)

scenario = json.loads(Path(sys.argv[1]).read_text())
results = []
for case in scenario['cases']:
    gate = namespace['Gate']()
    gate._agent_speech_started_at = case.get('agent')
    gate._active_vad_speech_started_at = case.get('vad')
    gate._backchannel_boundary = (0, case.get('cooldown', 1))
    gate._backchannel_boundary_timer = None
    gate._backchannel_boundary_callback = None
    gate._transcript_gate_active = True
    gate._transcript_buffer = deque(SimpleNamespace(
        type='final_transcript', request_id=e['id'], created_at=e['received'], speech_end_time=e.get('end')) for e in case.get('events', []))
    gate._hooks = MagicMock(interruption_by_audio_activity_enabled=False)
    gate._overlap_in_current_turn = True
    gate._interruption_enabled = True
    gate._interruption_ch = MagicMock(closed=False)
    gate._endpointing = MagicMock()
    gate._speaking = False
    gate._agent_speaking = True
    emitted = []
    gate._process_stt_event = lambda ev: emitted.append(ev.request_id)
    if case['action'] == 'trim':
        gate._trim_held_transcripts(resolved_at=case['resolved'], vad_speech_started_at=case.get('vad'))
    elif case['action'] == 'release':
        gate._flush_held_transcripts(resolved_at=case['resolved'], vad_speech_started_at=case.get('vad'))
    elif case['action'] in ('interruption', 'backchannel'):
        gate._on_overlap_speech_event(SimpleNamespace(
            is_interruption=case['action'] == 'interruption', detected_at=case['resolved'],
            overlap_started_at=case['overlap'], agent_ended=False))
    elif case['action'] == 'start':
        gate._on_start_of_agent_speech(case['agent'])
    elif case['action'] == 'rearm':
        gate._transcript_gate_active = False
        gate._on_start_of_overlap_speech(case['resolved'])
    elif case['action'] == 'cooldown':
        gate._backchannel_boundary = (1, 1)
        gate._transcript_gate_active = False
        gate._on_start_of_overlap_speech(case['agent'] + .5)
        gate._on_start_of_overlap_speech(case['agent'] + 2)
    results.append(dict(name=case['name'], held=[e.request_id for e in gate._transcript_buffer],
                        emitted=emitted, gate=gate._transcript_gate_active))
print(json.dumps(dict(contract=scenario['contract'], events=results)))
