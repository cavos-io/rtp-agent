#!/usr/bin/env python3
"""Generate offline v3.2 feature/inference evidence using the upstream algorithm.

Model source: pipecat-ai/smart-turn-v3, revision
f766f81d3cfdf7737ac64aad813d91bbfd56bf93 (CPU artifact).
Inference contract: pipecat-ai/smart-turn/inference.py; model filename there is
stale, so this runner requires the verified v3.2 artifact explicitly.
"""
import argparse
import hashlib
import json
from pathlib import Path

import numpy as np
import onnxruntime as ort
from transformers import WhisperFeatureExtractor

parser = argparse.ArgumentParser()
parser.add_argument('--model', required=True)
parser.add_argument('--output', required=True)
args = parser.parse_args()
assert hashlib.sha256(Path(args.model).read_bytes()).hexdigest() == '2bb026316b14a660486a75b1733cd3fbab8c2fd0314dc9af7be49f8cca967e4f'
session = ort.InferenceSession(args.model, providers=['CPUExecutionProvider'])
extractor = WhisperFeatureExtractor(chunk_length=8)
cases = []
for length, silence in [(16000, True), (48000, False), (144000, False)]:
    samples = np.zeros(length, dtype=np.float32) if silence else (((np.arange(length)*7919)%20001-10000).astype(np.float32)/32768)
    samples = samples[-128000:]
    samples = np.pad(samples, (128000-len(samples), 0))
    features = extractor(samples, sampling_rate=16000, return_tensors='np', padding='max_length', max_length=128000, truncation=True, do_normalize=True).input_features
    probability = float(session.run(None, {'input_features': features})[0].reshape(-1)[0])
    cases.append(dict(samples=length, silence=silence, features=features.reshape(-1).tolist(), probability=probability))
Path(args.output).write_text(json.dumps(cases))
print(json.dumps([dict(samples=c['samples'], probability=c['probability']) for c in cases]))
