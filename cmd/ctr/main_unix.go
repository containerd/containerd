// +build !windows

package main

func init() {
	extraCmds = append(extraCmds, shimCommand)
}
