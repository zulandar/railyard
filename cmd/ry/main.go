// Package main is the OSS railyard binary. It delegates to pkg/cli,
// where every subcommand and its dependencies live. Enterprise binaries
// follow the same pattern: side-effect import their plugins and call
// cli.Run().
package main

import "github.com/zulandar/railyard/pkg/cli"

func main() { cli.Run() }
