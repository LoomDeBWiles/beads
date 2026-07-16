# Build: Phase 1 — remove AGENTS.md writers from bd fork

## Acceptance

| Criterion | Command | Log | Pass |
|-----------|---------|-----|------|
| go build clean | go build ./... | [step1.log](step1.log) | Y |
| unit+regression tests | go test ./cmd/bd/... -run 'TestInit|TestFactory' -v | [step1.log](step1.log) | Y |

## Tests

7 matching top-level test groups passed: 22 init subtests and 5 Factory no-touch subtests. New coverage: TestInitDoesNotTouchAgentsFile exercises absent and sentinel AGENTS.md under default and quiet output; TestFactoryDoesNotTouchAgentsFile exercises all 3 install and 2 remove mutation paths.

## Notes

No divergence from the plan.

