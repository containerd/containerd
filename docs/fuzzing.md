# Fuzzing containerd

This document outlines the process for developing and testing new fuzzers for containerd, specifically focusing on integration with the OSS-Fuzz build system.

## 1. Fuzzer Location and Structure

New fuzz tests should generally reside in the `contrib/fuzz/` directory.

A typical Go fuzz test for containerd follows the `func FuzzX(f *testing.F)` signature.
Key components often include:
- `initDaemon` and `startDaemon`: For initializing and starting a containerd daemon instance for integration fuzzing.
- Helper functions: Fuzzers often rely on helper functions for common tasks, such as creating a `context.Context` with a timeout and appropriate namespace. For example, the `pullFuzzContext()` function (defined in `contrib/fuzz/pull_fuzz_test.go`) or the `fuzzContext()` function (defined in `contrib/fuzz/containerd_import_fuzz_test.go`).
- Mock implementations: For external dependencies like `remotes.Resolver` or `remotes.Fetcher` when fuzzing client-side logic, to control behavior during testing.

**Example (`FuzzPull` function from `pull_fuzz_test.go` snippet):**

```go
func FuzzPull(f *testing.F) {
	// ... client setup ...

	f.Fuzz(func(t *testing.T, data []byte) {
		initDaemon.Do(startDaemon) // Initialize a containerd daemon

		client, err := containerd.New(defaultAddress)
		if err != nil {
			// This can happen if the daemon is not ready
			return
		}
		defer client.Close()

		fuzzer := fuzz.NewConsumer(data)
		// ... fuzzer logic using fuzzer.GenerateStruct, fuzzer.GetBytes, etc. ...

		ctx, cancel := pullFuzzContext() // Use a helper function for context
		defer cancel()

		_, _ = client.Pull(ctx, "fuzz-image", containerd.WithResolver(resolver), containerd.WithPlatformMatcher(platforms.All))
	})
}
```

## 2. OSS-Fuzz Build Process

Containerd uses OSS-Fuzz for continuous fuzzing. The `infra/helper.py` script is used to build and run fuzzers locally within a Docker environment that mimics the OSS-Fuzz setup.

### Building Fuzzers Locally

To build all fuzzers, including newly added ones, use the `helper.py build_fuzzers` command from the **root** of the OSS-Fuzz repository:

```bash
cd /path/to/oss-fuzz
python3 infra/helper.py build_fuzzers containerd /path/to/your/local/containerd/repo
```

Replace `/path/to/your/local/containerd/repo` with the absolute path to your containerd repository checkout.

**Note:** The build process uses `git grep` to discover fuzzer functions. If you have added a new file containing fuzzers, you **must** stage it (e.g., using `git add path/to/your/new_fuzz_test.go`) before running the build, otherwise the new fuzzers will not be detected and compiled.

### Troubleshooting Build and Runtime Issues:

-   **`undefined: FuzzContext` or `redeclared in this block`**: If you define helper functions like `fuzzContext`, ensure they do not conflict with existing definitions in the same package. Renaming them (e.g., `pullFuzzContext`) or making them package-private is recommended.
-   **`inconsistent vendoring`**: After adding new dependencies (e.g., `go-fuzz-headers`), run `go mod tidy` followed by `go mod vendor` in your local repository to update `go.mod` and `vendor/modules.txt`.
-   **`ModuleNotFoundError: No module named 'constants'`**: Ensure you are running `infra/helper.py` from the root of the OSS-Fuzz repository checkout.
-   **Panics or Races in Goroutines**: If your fuzzer uses goroutines, avoid passing the `ConsumeFuzzer` (e.g., `github.com/AdaLogics/go-fuzz-headers`) object directly into them. Instead, extract all necessary data (using `GetBytes`, `GetString`, etc.) *before* starting the goroutines to prevent concurrent access and potential out-of-range panics.

## 3. Running Fuzzer Binaries

Once built, fuzzer binaries are placed in `build/out/containerd/` within the OSS-Fuzz repository.

To run a specific fuzzer (e.g., `fuzz_FuzzPull`) for a limited time (e.g., 10 seconds), run from the root of the OSS-Fuzz repository:

```bash
cd /path/to/oss-fuzz
python3 infra/helper.py run_fuzzer containerd fuzz_FuzzPull -- -max_total_time=10
```

### Verifying Bug Fixes

If you are verifying a fix for a crash (e.g., found in OSS-Fuzz or locally), you can run the reproducer file directly:

```bash
python3 infra/helper.py reproduce containerd <fuzz_target_name> <testcase_path>
```
