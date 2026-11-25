# Porch E2E API Tests

## Prerequisites
```bash
export E2E=1
```

## Running Tests

### Run Entire Suite
```bash
E2E=1 go test -v ./test/e2e/api
```

### Run Single Test
```bash
E2E=1 go test -v ./test/e2e/api -run "TestE2E/TestGitRepository"
```

### Run Table-Driven Sub-Test
```bash
E2E=1 go test -v ./test/e2e/api -run "TestE2E/TestBasicLifecycle/ProposeApprove"
```

### Pattern Matching
```bash
E2E=1 go test -v ./test/e2e/api -run "TestE2E/.*Evaluator"
```

### List All Available Tests
```bash
# List all test method names with source files
grep "func.*Test" ./test/e2e/api/*_test.go | grep -o "[^/]*_test.go:.*Test[A-Za-z0-9]*" | sort
```

## Test Structure

- **Suite-based**: All tests run under `TestE2E` using testify/suite
- **Individual methods**: `TestCloneFromUpstream`, `TestConcurrentEdits`, etc.
- **Table-driven sub-tests**: Some tests have sub-cases with `/` separator

## Function Images

Do not hardcode the `ghcr.io/kptdev/krm-functions-catalog` prefix in test pipelines.
Use short names (e.g. `set-namespace:v0.4.5`) so the porch server's `--default-image-prefix` applies and the tests can run without access to ghcr.

## Common Issues

1. **"no tests to run"**: Use correct test method name (e.g., `TestCloneFromUpstream`)
2. **Missing E2E env var**: Set `E2E=1` before running
3. **Timeout issues**: Tests may take several minutes in slow environments