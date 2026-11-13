import unittest
from unittest.mock import patch
from run import stop_all

class CleanupTests(unittest.TestCase):
    def test_failure_does_not_abandon_other_processes(self):
        calls=[]
        def stop(proc):
            calls.append(proc)
            if proc=='hung':
                raise RuntimeError('timeout')
        with patch('run.stop',stop):
            with self.assertRaises(ExceptionGroup):
                stop_all(['hung','receiver','server'])
        self.assertEqual(calls,['hung','receiver','server'])
