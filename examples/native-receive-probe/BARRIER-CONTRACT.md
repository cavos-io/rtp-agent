# Subscription barrier rerun contract

Only the isolated publisher startup handshake changes. The runner waits for
both native receivers' `subscribed` events before releasing the publisher via
an exclusive-created file in the fresh per-run directory. The publisher has
already published the track; it sends no media until released. Waits are bounded
at 20 seconds and publisher cancellation remains supported. Room readiness is
not sufficient. No receive policy or production transport changes.

Repeat ten continuous-silence baseline and ten 450ms publisher stalls at packet
566 using synchronized stats and socket traces, alternating receiver order.
Use the original ACCEPTANCE.md and ARRIVAL-CONTRACT.md gates unchanged. Add a
startup handshake verification: both subscribed timestamps must precede the
publisher release/start and first packet; complete traces must include packet 0.
All failed runs remain failed. No concurrent builds/analysis during capture.
Existing result files and contracts are hashed in results/barrier-preserved.json
before capture, then verified at completion. Use fresh results/barrier-baseline
and results/barrier-stall directories; never overwrite earlier evidence.
