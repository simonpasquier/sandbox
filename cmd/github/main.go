package main

import (
	"context"
	"flag"
	"fmt"
	"io/ioutil"
	"log"
	"os"
	"strings"

	"github.com/google/go-github/github"
	"github.com/jszwedko/go-circleci"
	"golang.org/x/oauth2"
)

var help bool
var ghtPath, cctPath string

func init() {
	flag.BoolVar(&help, "help", false, "Show help")
	flag.StringVar(&ghtPath, "token.github", "", "Path to your GitHub token")
	flag.StringVar(&cctPath, "token.circleci", "", "Path to your Circle CI token")
}

func checkMark(s string, ok bool) {
	check := "✗"
	if ok {
		check = "✔"
	}
	fmt.Printf("  %s %s\n", check, s)

}

func readTokenFile(name string) string {
	f, err := os.Open(name)
	if err != nil {
		log.Fatal(err)
	}
	token, err := ioutil.ReadAll(f)
	if err != nil {
		log.Fatal(err)
	}

	return strings.Trim(string(token), "\n")
}

func main() {
	flag.Parse()
	if help {
		fmt.Fprintln(os.Stderr, "Display a summary of GitHub integrations for all Prometheus projects.")
		flag.PrintDefaults()
		os.Exit(0)
	}
	if ghtPath == "" || cctPath == "" {
		log.Fatal("token.github and token.circleci parameters are mandataory")
	}

	ght := readTokenFile(ghtPath)
	cct := readTokenFile(cctPath)

	ctx := context.Background()
	ts := oauth2.StaticTokenSource(
		&oauth2.Token{AccessToken: ght},
	)
	tc := oauth2.NewClient(ctx, ts)

	ghc := github.NewClient(tc)
	ccc := circleci.Client{Token: cct}

	repos, _, err := ghc.Repositories.ListByOrg(ctx, "prometheus", nil)
	if err != nil {
		log.Fatal("Fail to list GitHub repositories:", err)
	}

	ccp, err := ccc.ListProjects()
	if err != nil {
		log.Fatal("Fail to list CircleCI projects:", err)
	}
	cc := make(map[string]circleci.FeatureFlags, len(repos))
	for _, p := range ccp {
		if "prometheus" == p.Username {
			cc[p.Reponame] = p.FeatureFlags
		}
	}

	for _, r := range repos {
		fmt.Println(r.GetName())

		fmt.Println()
		hooks, _, err := ghc.Repositories.ListHooks(ctx, "prometheus", r.GetName(), nil)
		if err != nil {
			log.Fatal("Fail to list hooks:", err)
		}

		fmt.Println("GitHub integrations")
		for _, h := range hooks {
			fmt.Println("ID:", h.GetID())
			var (
				name    string
				isValid bool
			)
			if h.GetName() == "web" {
				if u, ok := h.Config["url"]; ok {
					s, ok := u.(string)
					if ok {
						name = s + " (generic webhook)"
						isValid = true

					} else {
						name = "/!\\ URL field isn't a string"
					}
				} else {
					name = "/!\\ No URL in configuration"
				}
			} else {
				name = h.GetName() + " (GitHub service)"
			}
			checkMark(name, isValid)
			checkMark("active", h.GetActive())
			checkMark("events:"+strings.Join(h.Events, ","), true)
		}

		fmt.Println()
		fmt.Println("CircleCI")
		ccFlags, ok := cc[r.GetName()]
		if !ok {
			checkMark("Project not found in CircleCI", false)
		} else {
			checkMark("Build fork PRs", ccFlags.BuildForkPRs)
			checkMark("OSS", ccFlags.OSS)
		}

		fmt.Println("----")
	}
}
