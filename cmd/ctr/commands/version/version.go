package version

import (
	"fmt"
	"os"

	"github.com/containerd/containerd/cmd/ctr/commands"
	"github.com/google/cadvisor/version"
	"github.com/urfave/cli"
)

// Command is a cli ommand to output the client and containerd server version
var Command = cli.Command{
	Name:  "version",
	Usage: "print the client and server versions",
	Action: func(context *cli.Context) error {
		fmt.Println("Client:")
		fmt.Printf("  Version: %s\n", version.Version)
		fmt.Printf("  Revision: %s\n", version.Revision)
		fmt.Println("")
		client, ctx, cancel, err := commands.NewClient(context)
		if err != nil {
			return err
		}
		defer cancel()
		v, err := client.Version(ctx)
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
