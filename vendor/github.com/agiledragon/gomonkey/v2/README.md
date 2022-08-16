# gomonkey

gomonkey is a library to make monkey patching in unit tests easy, and the core idea of monkey patching comes from [Bouke](https://github.com/bouk), you can read [this blogpost](https://bou.ke/blog/monkey-patching-in-go/) for an explanation on how it works.

## Features

+ support a patch for a function
+ support a patch for a public member method
+ support a patch for a private member method
+ support a patch for a interface
+ support a patch for a function variable
+ support a patch for a global variable
+ support patches of a specified sequence for a function
+ support patches of a specified sequence for a member method
+ support patches of a specified sequence for a interface
+ support patches of a specified sequence for a function variable

## Notes
+ gomonkey fails to patch a function or a member method if inlining is enabled, please running your tests with inlining disabled by adding the command line argument that is `-gcflags=-l`(below go1.10) or `-gcflags=all=-l`(go1.10 and above).
+ A panic may happen when a goroutine is patching a function or a member method that is visited by another goroutine at the same time. That is to say, gomonkey is not threadsafe.

## Supported Platform:

- ARCH
  - amd64
  - arm64
  - 386

- OS
  - Linux
  - MAC OS X
  - Windows

## Installation
- below v2.1.0, for example v2.0.2
```go
$ go get github.com/agiledragon/gomonkey@v2.0.2
```
- v2.1.0 and above, for example v2.2.0
```go
$ go get github.com/agiledragon/gomonkey/v2@v2.2.0
```

## Test Method
```go
$ cd test 
$ go test -gcflags=all=-l
```

## Using gomonkey

Please refer to the test cases as idioms, very complete and detailed.

