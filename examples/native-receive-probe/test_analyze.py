import unittest
import numpy as np
from analyze import find_markers, packet_summary

class AnalysisTests(unittest.TestCase):
    def test_markers_identify_missing_duplicate_and_order(self):
        def tone(i):
            t=np.arange(6000)/24000
            return (10000*np.sin(2*np.pi*(600+i*60)*t)).astype(np.int16)
        pcm=np.concatenate([tone(0),np.zeros(12000),tone(2),np.zeros(12000),tone(2),np.zeros(12000),tone(1)])
        self.assertEqual([x['id'] for x in find_markers(pcm)], [0,2,2,1])
    def test_packet_gap_distinguishes_sparse_and_loss(self):
        sparse=packet_summary([{'seq':1,'ts':0},{'seq':2,'ts':48000}])
        loss=packet_summary([{'seq':1,'ts':0},{'seq':27,'ts':24960}])
        self.assertEqual(sparse['missing_sequences'],0)
        self.assertEqual(sparse['timestamp_gaps'],1)
        self.assertEqual(loss['missing_sequences'],25)
