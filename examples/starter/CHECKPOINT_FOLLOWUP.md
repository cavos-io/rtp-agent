# Checkpoint hook blocker follow-up

The prior checkpoint failed because Off compared function code addresses.
Closures created by the same factory can share code while capturing different
state. Removing the second callback therefore removed the first; an unregistered
closure could also remove an unrelated listener.

On, Once and Off now use the identity of the retained function value. The
registration-ID unsubscribe API is unchanged. This small compatibility helper
uses unsafe to access the gc compiler function-value pointer; it is a runtime
representation dependency, not a portable Go-language equality operation.
Callers should retain and reuse the same callback value with Off, or use the
unsubscribe function returned by On/Once. Re-evaluating a bound method is not
promised to produce the same function value.

The original failing test was reproduced unchanged. Added a noinline factory
regression to guarantee shared code addresses, covering On/Once, different
closures capturing the same channel, unregistered closure removal, callback
aliases and registration-ID unsubscribe. Both new cases failed before the fix.

Validation:

- Callback removal/Once tests passed 20 repetitions with race detection and
  checkptr=2.
- Full core/agent and starter package tests passed with localhost fixtures.
- Core package vet and diff checks passed.
- All 2915 historical hashes and the original failed-checkpoint patch checksum
  verified unchanged. The original failed reports remain historical evidence.

This resolves the listener test blocker documented in SHUTDOWN_RACE_FIX.md.
The separately observed multimodal lifecycle races remain for follow-up; no
full race-suite pass is claimed. The successful five-session rerun remains the
recorded evidence for the shutdown join fix; it was not repeated for this
callback identity change. No transport or playout policy was changed.
