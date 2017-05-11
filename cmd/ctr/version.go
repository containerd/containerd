package main

import (
	gocontext "context"
	"fmt"
	"os"

	"github.com/containerd/containerd/version"
	empty "github.com/golang/protobuf/ptypes/empty"
	"github.com/urfave/cli"
)

var versionCommand = cli.Command{
	Name:  "version",
	Usage: "print the version",
	Action: func(context *cli.Context) error {
		if context.NArg() > 0 {
			return fmt.Errorf("no argument expected")
		}
		fmt.Println("Client:")
		fmt.Printf("  Version: %s\n", version.Version)
		fmt.Printf("  Revision: %s\n", version.Revision)
		fmt.Println("")
		vs, err := getVersionService(context)
		if err != nil {
			return err
		}
		v, err := vs.Version(gocontext.Background(), &empty.Empty{})
		if err != nil {
			return err
		}
		fmt.Println("Server:")
		fmt.Printf("  Version: %s\n", v.Version)
		fmt.Printf("  Revision: %s\n", v.Revision)
		if v.Version != version.Version {
			fmt.Fprintf(os.Stderr, "WARNING: version mismatch\n")
		}
		if v.Revision != version.Revision {
			fmt.Fprintf(os.Stderr, "WARNING: revision mismatch\n")
		}
		return nil
	},
}
