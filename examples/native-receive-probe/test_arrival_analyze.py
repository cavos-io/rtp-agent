import unittest
from arrival_analyze import align_packets, synchronized, stats_points

class ArrivalTests(unittest.TestCase):
    def test_header_mapping_wraps_and_rejects_missing_duplicate_packets(self):
        sent=[dict(index=0,seq=65535,ts=2**32-960,at=100,sent=True),dict(index=1,seq=0,ts=0,at=200,sent=True)]
        trace=[dict(ssrc=42,seq=5,ts=500,at=110),dict(ssrc=42,seq=6,ts=1460,at=210)]
        mapped,meta=align_packets(sent,trace,42)
        self.assertEqual(mapped[1]['at'],210)
        self.assertEqual(meta['media_packets'],2)
        with self.assertRaises(ValueError):align_packets(sent,trace[:1],42)
        with self.assertRaises(ValueError):align_packets(sent,trace+[trace[0]],42)
        with self.assertRaises(ValueError):align_packets(sent,trace,43)

    def test_sync_rejects_scheduling_response_and_producer_skew(self):
        g=dict(slot=0,scheduled=100,requested=100,response_ms=2,capture=200)
        p=dict(g)
        self.assertTrue(synchronized([g],[p])[0]['valid'])
        for field,value in [('requested',20_000_100),('response_ms',21),('capture',30_000_200)]:
            self.assertFalse(synchronized([g],[dict(p,**{field:value})])[0]['valid'])
        with self.assertRaises(ValueError):synchronized([g],[dict(p,scheduled=200)])

    def test_native_timestamp_calibration_and_clock_step_rejection(self):
        anchors=[dict(event='clock_anchor',before=1_000_000_000,after=1_000_000_000,realtime=10_000_000_000),
                 dict(event='clock_anchor',before=2_000_000_000,after=2_000_000_000,realtime=11_000_000_000)]
        events=[]
        for slot in range(2):
            request=1_100_000_000+slot*100_000_000
            inbound=dict(jitter_buffer_emitted_count=4800*(slot+1),jitter_buffer_delay=144*(slot+1),
                jitter_buffer_target_delay=144*(slot+1),packets_discarded=0,concealed_samples=0,
                inserted_samples_for_deceleration=0,removed_samples_for_acceleration=0)
            record=dict(rtc={'timestamp':str((request+9_000_000_000+1_000_000)//1000)},
                stream=dict(kind='audio',ssrc=42),received=dict(packets_received=50,packets_lost=0),inbound=inbound)
            events.append(dict(event='native_stats',at=request+2_000_000,requested_at=request,scheduled=request,
                slot=slot,stats={'stats':[{'inbound_rtp':record}]}))
        points,clock=stats_points(anchors+events,'python')
        self.assertEqual(clock['offset_drift_ns'],0)
        self.assertEqual(points[0]['capture'],1_101_000_000)
        self.assertEqual(points[0]['capture_age_ms'],1)
        self.assertEqual(points[1]['buffer_ms'],30)
        anchors[1]['realtime']+=2_000_000
        with self.assertRaises(ValueError):stats_points(anchors+events,'python')
