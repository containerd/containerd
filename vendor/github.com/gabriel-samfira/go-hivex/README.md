# Golang Hivex bindings

Golang bindings for [hivex](https://github.com/libguestfs/hivex).

Minimum hivex version is ```1.3.14```

## Basic usage

```go
package main

import (
    "fmt"
    "log"
    "os"
    "path/filepath"

    hivex "github.com/gabriel-samfira/go-hivex"
)

func main() {
    gopath := os.Getenv("GOPATH")
    // There are a few test hives inside the package
    // Feel free to use your own
    hivePath := filepath.Join(
        gopath,
        "src/github.com/gabriel-samfira/go-hivex",
        "testdata",
        "rlenvalue_test_hive")

    // If you plan to write to the hive, replace hivex.READ
    // with hivex.WRITE. You may also enable verbose, debug or
    // unsafe (hivex.WRITE | hivex.DEBUG | hivex.UNSAFE)
    hive, err := hivex.NewHivex(hivePath, hivex.READ)
    if err != nil {
        log.Fatal(err)
    }

    root, err := hive.Root()
    if err != nil {
        log.Fatal(err)
    }
    // Get a child node called ModerateValueParent
    child, err := hive.NodeGetChild(root, "ModerateValueParent")
    if err != nil {
        log.Fatal(err)
    }
    // child will hold an int64 representing the offset of the node
    fmt.Println(child)

    // fetch the name of the Node
    childName, err := hive.NodeName(child)
    if err != nil {
        log.Fatal(err)
    }
    // print out the name (should be "ModerateValueParent")
    fmt.Println(childName)

    // Get the value offset of a key called "33Bytes" that lives in
    // \\ModerateValueParent
    valueOffset, err := hive.NodeGetValue(child, "33Bytes")
    if err != nil {
        log.Fatal(err)
    }

    // Get the actual value of that key. It should be a REG_BINARY (3)
    // with a value of 0123456789ABCDEF0123456789ABCDEF0
    valType, value, err := hive.ValueValue(valueOffset)
    if err != nil {
        log.Fatal(err)
    }
    fmt.Println(valType, string(value))
}
```

## Tests


The image files were shamelessly copied from the hivex package, and tests are based on the same tests in that package for the various other bindings.

```bash
$ go test github.com/gabriel-samfira/go-hivex/...
```
