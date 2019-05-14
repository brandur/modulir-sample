package main

import (
	"fmt"
	"os"
	"strings"

	"github.com/brandur/modulir"
	"github.com/spf13/cobra"
)

//////////////////////////////////////////////////////////////////////////////
//
//
//
// Main
//
//
//
//////////////////////////////////////////////////////////////////////////////

func main() {
	var rootCmd = &cobra.Command{
		Use:   "modulir-sample",
		Short: "Sample program demonstrating Modulir",
		Long: strings.TrimSpace(`
Sorg is a static site generator for Brandur's personal
homepage and some of its adjacent functions. See the product
in action at https://brandur.org.`),
	}
	rootCmd.AddCommand(&cobra.Command{
		Use:   "build",
		Short: "Run a single build loop",
		Long: strings.TrimSpace(`
Starts the build loop that watches for local changes and runs
when they're detected. A webserver is started on PORT (default
5004).`),
		Run: func(cmd *cobra.Command, args []string) {
			modulir.Build(getModulirConfig(), build)
		},
	})
	rootCmd.AddCommand(&cobra.Command{
		Use:   "loop",
		Short: "Start build and serve loop",
		Long: strings.TrimSpace(`
Runs the build loop one time and places the result in ./public.`),
		Run: func(cmd *cobra.Command, args []string) {
			modulir.BuildLoop(getModulirConfig(), build)
		},
	})

	if err := rootCmd.Execute(); err != nil {
		fmt.Fprintf(os.Stderr, "Error executing command: %v", err)
		os.Exit(1)
	}
}

//////////////////////////////////////////////////////////////////////////////
//
//
//
// Private
//
//
//
//////////////////////////////////////////////////////////////////////////////

// getModulirConfig interprets Conf to produce a configuration suitable to pass
// to a Modulir build loop.
func getModulirConfig() *modulir.Config {
	return &modulir.Config{
		Concurrency:    30,
		Log:            &modulir.Logger{Level: modulir.LevelInfo},
		Port:           5004,
		SourceDir:      ".",
		TargetDir:      "./public",
		Websocket:      true,
	}
}
