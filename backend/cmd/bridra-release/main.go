package main

import (
	"flag"
	"fmt"
	"os"
	"path/filepath"

	"github.com/cluion/bridra/backend/framework"
	"github.com/cluion/bridra/backend/internal/clirelease"
)

func main() {
	root := flag.String("root", ".", "Bridra backend module root")
	output := flag.String("output", "../build/bridra/cli", "release output directory")
	version := flag.String("version", framework.FrameworkVersion, "CLI release version")
	commit := flag.String("commit", "", "source commit identifier")
	buildDate := flag.String("build-date", "", "reproducible RFC 3339 build date")
	flag.Parse()
	if flag.NArg() != 0 {
		fmt.Fprintf(os.Stderr, "unexpected arguments: %v\n", flag.Args())
		os.Exit(2)
	}
	releaseOutput := filepath.Join(*output, *version)
	manifest, err := clirelease.Build(clirelease.Config{
		Root: *root, Output: releaseOutput, Version: *version,
		Commit: *commit, BuildDate: *buildDate,
	}, clirelease.DefaultSystem())
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	fmt.Printf("Bridra CLI %s release artifacts\n", manifest.Version)
	fmt.Printf("Output: %s\n", releaseOutput)
	for _, artifact := range manifest.Artifacts {
		fmt.Printf("%s  %s\n", artifact.SHA256, artifact.Archive)
	}
	fmt.Println("SHA256SUMS")
	fmt.Println("manifest.json")
}
