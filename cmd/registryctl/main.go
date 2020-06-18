package main

import (
	"flag"
	"fmt"
	"os"

	"github.com/heroku/docker-registry-client/registry"
)

var (
	help        bool
	registryURL string
	repository  string
)

func init() {
	flag.BoolVar(&help, "help", false, "Show help")
	flag.StringVar(&repository, "repository", "", "Name of the repository")
	flag.StringVar(&registryURL, "registry.url", "https://registry-1.docker.io/", "Registry URL")
}

func main() {
	flag.Parse()
	if help {
		fmt.Fprintln(os.Stderr, "check if the specified version of a container image matches with latest")
		flag.PrintDefaults()
		return
	}
	if repository == "" {
		fmt.Fprintln(os.Stderr, "expecting --repository flag")
		os.Exit(1)
	}

	args := flag.Args()
	if len(args) != 1 {
		fmt.Fprintln(os.Stderr, "expecting one argument")
		os.Exit(1)
	}

	hub, err := registry.New(
		registryURL,
		"", // no username (anonymous).
		"", // no password.
	)
	//tags, err := hub.Tags(repository)
	//if err != nil {
	//	fmt.Fprintf(os.Stderr, "failed to query tags: %v\n", err)
	//}

	latestManifest, err := hub.ManifestV2(repository, "latest")
	if err != nil {
		fmt.Fprintf(os.Stderr, "failed to query latest tag: %v\n", err)
		os.Exit(1)
	}

	version := flag.Arg(0)
	versionManifest, err := hub.ManifestV2(repository, version)
	if err != nil {
		fmt.Fprintf(os.Stderr, "failed to query %q tag: %v\n", version, err)
		os.Exit(1)
	}

	if latestManifest.Layers[len(latestManifest.Layers)-1].Digest != versionManifest.Layers[len(versionManifest.Layers)-1].Digest {
		fmt.Fprintf(os.Stderr, "tag %q isn't the same as 'latest'\n", version)
		for i, l := range versionManifest.Layers {
			fmt.Fprintf(os.Stderr, "%s:%d: %s\n", version, i, l.Digest)
		}
		for i, l := range latestManifest.Layers {
			fmt.Fprintf(os.Stderr, "latest:%d: %s\n", i, l.Digest)
		}
		os.Exit(1)
	}
	fmt.Printf("%q and 'latest' tags have the same digests.\n", version)
}
