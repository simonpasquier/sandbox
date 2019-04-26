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
	"github.com/pkg/errors"
	"golang.org/x/oauth2"
)

var (
	help             bool
	ghtPath, cctPath string
	org              string
)

func init() {
	flag.BoolVar(&help, "help", false, "Show help")
	flag.StringVar(&cctPath, "circleci.token", "", "Path to your Circle CI token")
	flag.StringVar(&ghtPath, "github.token", "", "Path to your GitHub token")
	flag.StringVar(&org, "github.organization", "prometheus", "Name of the GitHub organization")
}

func checkMark(s string, ok bool) {
	check := "✗"
	if ok {
		check = "✔"
	}
	fmt.Printf("  %s %s\n", check, s)

}

func readTokenFile(name string) (string, error) {
	f, err := os.Open(name)
	if err != nil {
		return "", err
	}
	token, err := ioutil.ReadAll(f)
	if err != nil {
		return "", err
	}

	return strings.Trim(string(token), "\n"), nil
}

func main() {
	flag.Parse()
	if help {
		fmt.Fprintln(os.Stderr, "Show a summary of settings for your GitHub organization's projects.")
		flag.PrintDefaults()
		os.Exit(0)
	}
	if ghtPath == "" {
		log.Fatal("github.token parameters is mandataory")
	}
	if cctPath == "" {
		log.Println("WARN: circleci.token parameter is missing")
	}
	if err := run(org, ghtPath, cctPath); err != nil {
		log.Fatal(err)
	}
}

func run(org string, ghToken string, ccToken string) error {
	ght, err := readTokenFile(ghToken)
	if err != nil {
		return err
	}
	ctx := context.Background()
	ghc := github.NewClient(
		oauth2.NewClient(
			ctx,
			oauth2.StaticTokenSource(
				&oauth2.Token{AccessToken: ght},
			),
		),
	)

	var ccc *circleci.Client
	if ccToken != "" {
		cct, err := readTokenFile(ccToken)
		if err != nil {
			return err
		}
		ccc = &circleci.Client{Token: cct}
	}

	var repos = make([]*github.Repository, 0)
	opt := &github.RepositoryListByOrgOptions{
		ListOptions: github.ListOptions{PerPage: 10},
	}
	for {
		r, resp, err := ghc.Repositories.ListByOrg(ctx, org, opt)
		if err != nil {
			return errors.Wrap(err, "Fail to list GitHub repositories")
		}
		repos = append(repos, r...)
		if resp.NextPage == 0 {
			break
		}
		opt.Page = resp.NextPage
	}
	fmt.Printf("✔ Found %d repositories in GitHub\n\n", len(repos))

	cc := make(map[string]circleci.FeatureFlags)
	if ccc != nil {
		ccp, err := ccc.ListProjects()
		if err != nil {
			return errors.Wrap(err, "fail to list CircleCI projects")
		}
		for _, p := range ccp {
			if "prometheus" == p.Username {
				cc[p.Reponame] = p.FeatureFlags
			}
		}
	}

	for _, r := range repos {
		fmt.Println("Repository:", r.GetName())

		fmt.Println()
		hooks, _, err := ghc.Repositories.ListHooks(ctx, "prometheus", r.GetName(), nil)
		if err != nil {
			return errors.Wrap(err, "fail to list hooks")
		}

		//TODO: list branch settings.
		//TODO: list deploy keys.
		//TODO: list security alerts.

		fmt.Println("External integrations:")
		for _, h := range hooks {
			fmt.Println("• ID:", h.GetID())
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
				isValid = true
			}
			checkMark(name, isValid)
			checkMark("active", h.GetActive())
			checkMark(fmt.Sprintf("events: %s", strings.Join(h.Events, ",")), true)
		}
		//TODO: list installed apps.

		if len(cc) > 0 {
			fmt.Println()
			fmt.Println("CircleCI")
			ccFlags, ok := cc[r.GetName()]
			if !ok {
				checkMark("Project not found in CircleCI", false)
			} else {
				checkMark("Build fork PRs", ccFlags.BuildForkPRs)
				checkMark("OSS", ccFlags.OSS)
			}
		}

		fmt.Println()
	}
	return nil
}
