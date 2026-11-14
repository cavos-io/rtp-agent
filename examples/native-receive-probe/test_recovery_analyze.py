import unittest
from recovery_analyze import recovery

class RecoveryTests(unittest.TestCase):
    def test_requires_sustained_window_and_reports_rebound(self):
        points=[dict(t=i/10,value=80 if i<5 or i==27 else 20,valid=True) for i in range(35)]
        r=recovery(points,30,0)
        self.assertAlmostEqual(r['begin_s'],.5)
        self.assertAlmostEqual(r['confirmed_s'],2.5)
        self.assertEqual(r['later_excursions'],1)

    def test_invalid_sample_breaks_confirmation(self):
        points=[dict(t=i/10,value=20,valid=i!=10) for i in range(30)]
        self.assertIsNone(recovery(points,30,0)['confirmed_s'])

    def test_gap_and_high_values_are_censored(self):
        points=[dict(t=i/10,value=20,valid=True) for i in list(range(10))+list(range(20,35))]
        self.assertIsNone(recovery(points,30,0)['confirmed_s'])
        self.assertIsNone(recovery([dict(t=i/10,value=80,valid=True) for i in range(40)],30,0)['confirmed_s'])

    def test_marker_confirmation_requires_three_consecutive_ids(self):
        from recovery_analyze import marker_recovery
        markers=[dict(id=i,source_s=i+10,callback_ms=20) for i in [10,12,13]]
        self.assertIsNone(marker_recovery(markers,70,11.77)['observed_by_s'])
        markers.append(dict(id=14,source_s=24,callback_ms=20))
        r=marker_recovery(markers,70,11.77)
        self.assertEqual(r['first_marker'],12)
        self.assertAlmostEqual(r['observed_by_s'],10.23)

    def test_counter_roundoff_does_not_create_false_excursions(self):
        points=[dict(t=i/10,value=40+1e-10,valid=True) for i in range(30)]
        self.assertIsNotNone(recovery(points,40,0)['confirmed_s'])
        points=[dict(t=i/10,value=40.001,valid=True) for i in range(30)]
        self.assertIsNone(recovery(points,40,0)['confirmed_s'])
