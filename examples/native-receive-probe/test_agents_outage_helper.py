"""IPC framing tests only; no mocked receive-engine claim."""
import unittest
from types import SimpleNamespace
from agents_outage_helper import ipc_chunks

class FramingTest(unittest.TestCase):
    def test_preserves_native_and_framework_frames(self):
        for size in (2400, 24000):
            for data in (bytes(size), bytes(i % 251 for i in range(size))):
                frame=SimpleNamespace(data=data,sample_rate=24000,num_channels=1)
                chunks=list(ipc_chunks(frame))
                self.assertEqual(b"".join(chunks),data)
                self.assertEqual(len(chunks),size//2400)
                self.assertTrue(all(len(c)==2400 for c in chunks))

    def test_rejects_shape_instead_of_padding(self):
        with self.assertRaises(RuntimeError):
            list(ipc_chunks(SimpleNamespace(data=b"x",sample_rate=24000,num_channels=1)))

if __name__ == "__main__":
    unittest.main()
