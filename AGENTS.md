## Code rules
- Code must be clear enoguh to avoid code comments
- No code comments
## Tests

- A test SHOULD earn its place by guarding a behaviour that a plausible change could break and no other test catches. Tests SHOULD check behaviour. They SHOULD NOT test code a reader can verify, wiring, constants or library behaviour.
- Tests SHOULD assert relations ("inside", "wider than", "changed"). They SHOULD NOT assert exact values we chose or timings.
- A bug fix SHOULD add a test that fails without it, unless the fixed code is a few lines you can check by reading. A crash fix SHOULD also add its input to the crate's hostile-input sweep.
- Integration tests SHOULD be one binary per crate, `tests/it/main.rs`.
- You MUST NOT delete, weaken, ignore or skip a test to make a check pass.
- Before adding tests beyond these rules, or cutting or quarantining any test, read standards/testing.md. Cuts and quarantines SHOULD be in a PR of their own, which MUST list every removed test.
