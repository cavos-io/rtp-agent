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

class SubscriptionTests(unittest.TestCase):
    def test_room_ready_alone_does_not_release_publisher(self):
        import json
        import tempfile
        from pathlib import Path
        from unittest.mock import Mock
        from run import release_publisher
        with tempfile.TemporaryDirectory() as temp:
            dest=Path(temp)
            for side in ('go','python'):
                (dest/f'{side}.jsonl').write_text(json.dumps({'event':'ready'})+'\n')
            proc=Mock();proc.poll.return_value=None
            slept=[]
            def progress(_):
                self.assertFalse((dest/'subscriptions-ready').exists())
                side='go' if not slept else 'python'
                with (dest/f'{side}.jsonl').open('a') as f:
                    f.write(json.dumps({'event':'subscribed'})+'\n')
                slept.append(side)
            with patch('run.time.sleep',progress):
                release_publisher([('go',proc),('python',proc)],proc,dest)
            self.assertEqual(slept,['go','python'])
            self.assertTrue((dest/'subscriptions-ready').exists())

    def test_failed_receiver_does_not_release_publisher(self):
        import tempfile
        from pathlib import Path
        from unittest.mock import Mock
        from run import release_publisher
        with tempfile.TemporaryDirectory() as temp:
            dest=Path(temp)
            proc=Mock();proc.poll.return_value=1
            with self.assertRaises(RuntimeError):
                release_publisher([('go',proc)],proc,dest)
            self.assertFalse((dest/'subscriptions-ready').exists())
