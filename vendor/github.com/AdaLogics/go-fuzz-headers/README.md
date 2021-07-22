# go-fuzz-headers
This repository contains various helper functions to be used with [go-fuzz](https://github.com/dvyukov/go-fuzz).

## Goal

The current goal of go-fuzz-headers is:
To maintain a series of helper utilities that can be used for golang projects that are integrated into OSS-fuzz and use the go-fuzz engine to fuzz more complicated types than merely strings and data arrays. 
While go-fuzz-headers can be used when using go-fuzz outside of OSS-fuzz, we do not test such usage and cannot confirm that it is supported.

## Status

The project is under development and will be updated regularly.

Fuzzers that use `GenerateStruct` will not require modifications as more types get supported.

## Usage
To make use of the helper functions, a ConsumeFuzzer has to be instantiated:
```go
f := NewConsumer(data)
```
To split the input data from the fuzzer into a random set of equally large chunks:
```go
err := f.Split(3, 6)
```
...after which the consumer has the following available attributes:
```go
f.CommandPart = commandPart
f.RestOfArray = restOfArray
f.NumberOfCalls = numberOfCalls
```
To pass the input data from the fuzzer into a struct:
```go
ts := new(target_struct)
err :=f.GenerateStruct(ts)
```
or:
```go
ts := target_struct{}
err = f.GenerateStruct(&ts)
```
`GenerateStruct` will pass data from the input data to the targeted struct. Currently the following field types are supported:
1. `string`
2. `bool`
3. `int`
4. `[]string`
5. `byte`
5. `[]byte`
6. custom structures
7. `map`
