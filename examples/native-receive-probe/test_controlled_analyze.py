import unittest
import json
import tempfile
from pathlib import Path
import numpy as np
from analyze import find_markers
from controlled_analyze import align_observer, callback_pairing, native_stats


class AttributionTests(unittest.TestCase):
    def test_sfu_rewrite_and_dummy_packets_are_not_media(self):
        sent=[dict(seq=65535,ts=2**32-960,bytes=70,sent=True),
              dict(seq=0,ts=0,bytes=80,sent=True)]
        obs=[dict(seq=1,ts=20,bytes=1),dict(seq=5,ts=500,bytes=70),
             dict(seq=6,ts=1460,bytes=80),dict(seq=7,ts=1461,bytes=1)]
        _,matched,prefix=align_observer(sent,obs)
        self.assertEqual(prefix,1)
        self.assertEqual(matched,obs[1:3])
        with self.assertRaises(ValueError):
            align_observer(sent,obs[:2])

    def test_callback_pairing_rejects_missing_or_reordered_delivery(self):
        frames=[dict(samples=1200,at=100),dict(samples=1200,at=200)]
        callbacks=[dict(samples=1200,at=90,stream=1),dict(samples=1200,at=180,stream=1)]
        self.assertEqual(callback_pairing(frames,callbacks)['max_ms'],.00002)
        with self.assertRaises(ValueError):
            callback_pairing(frames,callbacks[:1])
        with self.assertRaises(ValueError):
            callback_pairing(frames,list(reversed(callbacks)))

    def test_one_marker_split_by_gap_is_scored_twice_by_original_detector(self):
        # This demonstrates scoring ambiguity, not real receive-engine behavior.
        tone=(10000*np.sin(2*np.pi*1080*np.arange(6000)/24000)).astype(np.int16)
        self.assertEqual([m['id'] for m in find_markers(tone)],[8])
        split=np.concatenate([tone[:3000],np.zeros(8000,dtype=np.int16),tone[3000:]])
        self.assertEqual([m['id'] for m in find_markers(split)],[8,8])

    def test_native_counter_intervals_use_deltas_not_process_clocks(self):
        inbound=dict(jitter_buffer_delay=48, jitter_buffer_target_delay=96,
            jitter_buffer_emitted_count=48000, concealed_samples=100,
            silent_concealed_samples=0, concealment_events=1,
            inserted_samples_for_deceleration=0, removed_samples_for_acceleration=0,
            packets_discarded=0)
        later=dict(inbound,jitter_buffer_delay=144,jitter_buffer_target_delay=240,
            jitter_buffer_emitted_count=96000,concealed_samples=340)
        events=[dict(event='native_stats',at=0,stats={})]
        for at,stats in [(100, inbound),(1_000_000_100,later)]:
            events.append(dict(event='native_stats',at=at,stats={'stats':[{'inbound_rtp':dict(
                stream={'kind':'audio'}, received=dict(packets_received=50,packets_lost=0,jitter=0),
                inbound=stats)}]}))
        with tempfile.TemporaryDirectory() as d:
            path=Path(d)/'stats.jsonl'
            path.write_text(''.join(json.dumps(e)+'\n' for e in events))
            result=native_stats(path,100)
        self.assertEqual(result[1]['at_s'],1)
        self.assertEqual(result[1]['interval_jitter_buffer_ms'],2)
        self.assertEqual(result[1]['interval_target_ms'],3)
        self.assertEqual(result[1]['interval_concealed_samples'],240)
