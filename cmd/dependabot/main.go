package main

import (
	"context"
	"flag"
	"fmt"
	"io/ioutil"
	"log"
	"os"
	"strings"
	"sync"

	"github.com/google/go-github/v24/github"
	"github.com/pkg/errors"
	"golang.org/x/oauth2"
)

var (
	help          bool
	ghtPath       string
	ghUser        string
	ghUpstreamOrg string
)

const (
	ghOrg           = "prometheus"
	dependabotLabel = "dependencies"
)

func init() {
	flag.BoolVar(&help, "help", false, "Show help")
	flag.StringVar(&ghtPath, "github.token", "", "Path to your GitHub token")
	flag.StringVar(&ghUser, "github.user", "", "Your GitHub user")
	flag.StringVar(&ghUpstreamOrg, "github.upstream_org", "prometheus", "The upstream organization")
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
		fmt.Fprintln(os.Stderr, "Sanitize and publish dependabot PRs.")
		flag.PrintDefaults()
		os.Exit(0)
	}
	if ghtPath == "" {
		log.Fatal("github.token parameter is mandatory")
	}
	if ghUser == "" {
		log.Fatal("github.user parameter is mandatory")
	}
	if ghUpstreamOrg == "" {
		log.Fatal("github.upstream_org parameter is mandatory")
	}

	ght := readTokenFile(ghtPath)

	ctx := context.Background()
	ts := oauth2.StaticTokenSource(
		&oauth2.Token{AccessToken: ght},
	)
	ghc := github.NewClient(oauth2.NewClient(ctx, ts))

	// Retrieve all repositories forked from the upstream organization.
	fmt.Printf("Retrieving %q organization's forks for the %q user\n", ghUpstreamOrg, ghUser)
	opt := &github.RepositoryListOptions{
		Affiliation: "owner",
		ListOptions: github.ListOptions{PerPage: 50},
	}

	var (
		wg     = sync.WaitGroup{}
		repoCh = make(chan *github.Repository)
		forks  = make([]*github.Repository, 0)
	)
	for {
		repos, resp, err := ghc.Repositories.List(ctx, ghUser, opt)
		if err != nil {
			log.Fatal(errors.Wrapf(err, "failed to list repositories for %s", ghUser))
		}
		for _, repo := range repos {
			wg.Add(1)
			go func(repo *github.Repository) {
				defer wg.Done()
				if !repo.GetFork() {
					return
				}
				repo, _, err := ghc.Repositories.Get(ctx, ghUser, repo.GetName())
				if err != nil {
					fmt.Println("✗", errors.Wrapf(err, "failed to get repository %s", repo.GetName()))
					return
				}
				if repo.Source.Owner.GetLogin() == ghUpstreamOrg {
					repoCh <- repo
				}
			}(repo)
		}
		if resp.NextPage == 0 {
			break
		}
		opt.Page = resp.NextPage
	}

	go func() {
		wg.Wait()
		close(repoCh)
	}()
	for repo := range repoCh {
		forks = append(forks, repo)
	}
	fmt.Printf("✔ Found %d forks\n", len(forks))

	prCh := make(chan *dependabotPullRequest, 1)
	wg.Add(1)
	go func() {
		defer wg.Done()
		for pr := range prCh {
			err := pr.doWork()
			if err != nil {
				fmt.Printf("x got %q when processing %s\n", err, pr)
			}
			fmt.Printf("✔ %s processed\n", pr)
		}
	}()

	for _, fork := range forks {
		n := 0
		opt := github.PullRequestListOptions{ListOptions: github.ListOptions{PerPage: 10}}
		for {
			ghPRs, resp, err := ghc.PullRequests.List(ctx, fork.Owner.GetLogin(), fork.GetName(), &opt)
			if err != nil {
				fmt.Println("x", errors.Wrapf(err, "failed to list pull requests from %s", fork.GetName()))
				continue
			}
			for _, pr := range ghPRs {
				for _, l := range pr.Labels {
					if l.GetName() == dependabotLabel {
						n++
						prCh <- &dependabotPullRequest{ctx: ctx, c: ghc, pr: pr, upstream: ghUpstreamOrg}
						break
					}
				}
			}
			if resp.NextPage == 0 {
				break
			}
			opt.Page = resp.NextPage
		}
		fmt.Printf("✔ Found %d dependabot PRs in %s/%s\n", n, fork.Owner.GetLogin(), fork.GetName())
	}

	close(prCh)
	wg.Done()
}

type dependabotPullRequest struct {
	ctx context.Context
	c   *github.Client
	pr  *github.PullRequest
	// The name of the upstream repository.
	upstream string
}

func (d *dependabotPullRequest) userName() string {
	return d.pr.Head.User.GetLogin()
}

func (d *dependabotPullRequest) repoName() string {
	return d.pr.Head.Repo.GetName()
}

func (d *dependabotPullRequest) branchName() string {
	return d.pr.Head.GetLabel()
}

func (d *dependabotPullRequest) sha() string {
	return d.pr.Head.GetSHA()
}

func (d *dependabotPullRequest) existsUpstream() (bool, error) {
	opt := github.PullRequestListOptions{Head: d.branchName()}
	prs, _, err := d.c.PullRequests.List(d.ctx, d.upstream, d.repoName(), &opt)
	if err != nil {
		return false, err
	}
	return len(prs) > 0, nil
}

func (d *dependabotPullRequest) checkStatus() (bool, error) {
	status, _, err := d.c.Repositories.GetCombinedStatus(d.ctx, d.userName(), d.repoName(), d.sha(), nil)
	if err != nil {
		return false, err
	}
	if status.GetTotalCount() == 0 {
		return false, errors.New("no status found")
	}
	return status.GetState() == "succes", nil
}

func (d *dependabotPullRequest) runScript() error {
	return nil
}

func (d *dependabotPullRequest) sendUpstream() error {
	return nil
}

func (d *dependabotPullRequest) String() string {
	return fmt.Sprintf("%s @ %s", d.repoName(), d.branchName())
}

func (d *dependabotPullRequest) doWork() error {
	// Check whether the PR is already opened in the upstream repository.
	if exists, err := d.existsUpstream(); err != nil {
		return err
	} else if exists {
		fmt.Printf("%s: already exists in the upstream repository\n", d)
		return nil
	}

	// Check whether Circle CI/Travis CI are happy.
	ok, err := d.checkStatus()
	if err != nil {
		return err
	}

	if !ok {
		// Launch the update script.
		// PULL_REQUEST_BRANCH
		// REPOSITORY_NAME
		fmt.Printf("%s: runScript()\n", d)
		return d.runScript()
	}

	// Open the PR in the upstream repository.
	fmt.Printf("%s: sendUpstream()\n", d)
	return d.sendUpstream()
}
