go run main.go -func FuzzFoo -o fuzzer.a github.com/AdamKorcz/go-118-fuzz-build/fuzzers/vitess
clang -o fuzzer fuzzer.a -fsanitize=fuzzer
