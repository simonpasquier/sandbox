package main

import (
	"bytes"
	"context"
	"flag"
	"fmt"
	"io/ioutil"
	"log"
	"os"
	"os/exec"
	"strings"
	"sync"
	"time"

	"github.com/google/go-github/github"
	"github.com/pkg/errors"
	"golang.org/x/oauth2"
)

var (
	help            bool
	dryrun          bool
	recreateMissing bool
	ghtPath         string
	ghUser          string
	ghUpstreamOrg   string
	updateScript    string
)

const (
	dependabotLabel = "dependencies"
	concurrency     = 4
)

func init() {
	flag.BoolVar(&help, "help", false, "Show help")
	flag.BoolVar(&dryrun, "dry-run", false, "Dry-run mode")
	flag.BoolVar(&recreateMissing, "recreate-missing-checks", false, "Recreate the pull request when checks are missing")
	flag.StringVar(&ghtPath, "github.token", "", "Path to your GitHub token")
	flag.StringVar(&ghUser, "github.user", "", "Your GitHub user")
	flag.StringVar(&ghUpstreamOrg, "github.upstream_org", "prometheus", "The upstream organization")
	flag.StringVar(&updateScript, "script", "", "Script to run on dependabot pull requests")
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

	prCh := make(chan *dependabotPullRequest, concurrency)
	wg.Add(concurrency)
	for i := 0; i < concurrency; i++ {
		go func() {
			defer wg.Done()
			for pr := range prCh {
				status, err := pr.currentStatus()
				if err != nil {
					fmt.Printf("✗ %s: %s\n", pr, err)
					continue
				}

				switch {
				case status == upstreamStatus:
					fmt.Printf("✔ %s: already opened upstream\n", pr)

				case status == notMergeableStatus || (recreateMissing && status == missingCheckStatus):
					err = pr.recreate()
					if err != nil {
						fmt.Printf("✗ %s: %s\n", pr, err)
						break
					} else {
						err = pr.addLabel(rebaseLabel)
						if err != nil {
							fmt.Printf("✗ %s: %s\n", pr, err)
							break
						}
					}
					fmt.Printf("✔ %s: pending recreate operation\n", pr)

				case status == mergeableStatus:
					err = pr.removeLabel(rebaseLabel)
					if err != nil {
						fmt.Printf("✗ %s: %s\n", pr, err)
						break
					}
					fmt.Printf("✔ %s: %q label removed\n", pr, rebaseLabel)

				case status == missingCheckStatus:
					fmt.Printf("✗ %s: missing checks\n", pr)

				case status == failedCheckStatus:
					// TODO: add a label to run script only once.
					err := pr.runScript()
					if err != nil {
						fmt.Printf("✗ %s: %s\n", pr, err)
						break
					}
					fmt.Printf("✔ %s: update script executed\n", pr)

				case status == pendingCheckStatus:
					fmt.Printf("✔ %s: pending checks\n", pr)

				case status == okCheckStatus:
					err := pr.sendUpstream()
					if err != nil {
						fmt.Printf("✗ %s: %s\n", pr, err)
						break
					} else {
						err = pr.addLabel(upstreamLabel)
						if err != nil {
							fmt.Printf("✗ %s: %s\n", pr, err)
							break
						}
					}
					fmt.Printf("✔ %s: checks ok\n", pr)
				}
			}
		}()
	}

	for _, fork := range forks {
		// TODO: check that the fork is synchronized with upstream.
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
						pr := &dependabotPullRequest{
							ctx:          ctx,
							c:            ghc,
							pr:           pr,
							upstream:     ghUpstreamOrg,
							updateScript: updateScript,
							dryrun:       dryrun,
						}
						prCh <- pr
						break
					}
				}
			}
			if resp.NextPage == 0 {
				break
			}
			opt.Page = resp.NextPage
		}
	}

	close(prCh)
	wg.Wait()
}

type prStatus string

const (
	unknownStatus      = prStatus("unknown")              // check the error
	missingCheckStatus = prStatus("missing checks")       // recreate (maybe?)
	pendingCheckStatus = prStatus("waiting for checks")   // nothing, retry later
	failedCheckStatus  = prStatus("failed checks")        // should run update
	okCheckStatus      = prStatus("checks ok")            // should open PR upstream
	notMergeableStatus = prStatus("not mergeable")        // should recreate
	mergeableStatus    = prStatus("mergeable")            // should remove label
	upstreamStatus     = prStatus("waiting for upstream") // nothing to do
)

type dependabotPullRequest struct {
	ctx context.Context
	c   *github.Client
	pr  *github.PullRequest
	// The name of the upstream repository.
	upstream     string
	updateScript string
	dryrun       bool
}

func (d *dependabotPullRequest) userName() string {
	return d.pr.Head.User.GetLogin()
}

func (d *dependabotPullRequest) repoName() string {
	return d.pr.Head.Repo.GetName()
}

func (d *dependabotPullRequest) branchName() string {
	return d.pr.Head.GetRef()
}

func (d *dependabotPullRequest) sha() string {
	return d.pr.Head.GetSHA()
}

func (d *dependabotPullRequest) getUpstreamPR() (string, error) {
	opt := github.PullRequestListOptions{Head: d.pr.Head.GetLabel(), State: "closed"}
	prs, _, err := d.c.PullRequests.List(d.ctx, d.upstream, d.repoName(), &opt)
	if err != nil {
		return "", err
	}
	for _, pr := range prs {
		merged, _, err := d.c.PullRequests.IsMerged(d.ctx, d.upstream, d.repoName(), pr.GetNumber())
		if err != nil {
			return "", err
		}
		if merged {
			return pr.GetURL(), nil
		}
	}

	opt.State = "open"
	prs, _, err = d.c.PullRequests.List(d.ctx, d.upstream, d.repoName(), &opt)
	if err != nil {
		return "", err
	}
	if len(prs) == 1 {
		return prs[0].GetURL(), nil
	}
	return "", nil
}

func (d *dependabotPullRequest) isMergeable() (bool, error) {
	var i = 0
	for d.pr.Mergeable == nil && i < 3 {
		time.Sleep(1 * time.Second)
		pr, _, err := d.c.PullRequests.Get(d.ctx, d.userName(), d.repoName(), d.pr.GetNumber())
		if err != nil {
			return false, err
		}
		d.pr = pr
		i++
	}
	if d.pr.Mergeable == nil {
		return false, errors.Errorf("fail to check mergeability of %s", d.pr.GetURL())
	}
	return d.pr.GetMergeable(), nil
}

const (
	rebaseLabel   = "needs rebase"
	upstreamLabel = "upstream pr"
)

var recreateCommand = "@dependabot recreate"

func (d *dependabotPullRequest) recreate() error {
	if d.dryrun {
		return nil
	}
	if d.isLabelPresent(rebaseLabel) {
		return nil
	}
	comment := github.IssueComment{
		Body: &recreateCommand,
	}
	_, _, err := d.c.Issues.CreateComment(d.ctx, d.userName(), d.repoName(), d.pr.GetNumber(), &comment)
	return err
}

func (d *dependabotPullRequest) addLabel(label string) error {
	if d.dryrun {
		return nil
	}
	labels := make([]string, 0, len(d.pr.Labels))
	for _, l := range d.pr.Labels {
		if l.GetName() == label {
			return nil
		}
		labels = append(labels, l.GetName())
	}
	labels = append(labels, label)
	update := &github.IssueRequest{
		Labels: &labels,
	}
	_, _, err := d.c.Issues.Edit(d.ctx, d.userName(), d.repoName(), d.pr.GetNumber(), update)
	return err
}

func (d *dependabotPullRequest) removeLabel(label string) error {
	if d.dryrun {
		return nil
	}
	if len(d.pr.Labels) == 0 {
		// No label.
		return nil
	}
	labels := make([]string, 0, len(d.pr.Labels)-1)
	for _, l := range d.pr.Labels {
		if l.GetName() == label {
			continue
		}
		labels = append(labels, l.GetName())
	}
	if len(labels) == len(d.pr.Labels) {
		// Label not found.
		return nil
	}
	update := &github.IssueRequest{
		Labels: &labels,
	}
	_, _, err := d.c.Issues.Edit(d.ctx, d.userName(), d.repoName(), d.pr.GetNumber(), update)
	return err
}

func (d *dependabotPullRequest) isLabelPresent(label string) bool {
	for _, l := range d.pr.Labels {
		if l.GetName() == label {
			return true
		}
	}
	return false
}

func (d *dependabotPullRequest) checkStatus() (string, error) {
	status, _, err := d.c.Repositories.GetCombinedStatus(d.ctx, d.userName(), d.repoName(), d.sha(), nil)
	if err != nil {
		return "", err
	}
	if status.GetTotalCount() == 0 {
		return "", nil
	}
	return status.GetState(), nil
}

func (d *dependabotPullRequest) runScript() error {
	if d.dryrun || d.updateScript == "" {
		return nil
	}
	var (
		stdout bytes.Buffer
		stderr bytes.Buffer
	)
	cmd := exec.CommandContext(d.ctx, d.updateScript)
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	cmd.Env = append(os.Environ(),
		fmt.Sprintf("GITHUB_OWNER=%s", d.userName()),
		fmt.Sprintf("GITHUB_REPOSITORY=%s", d.repoName()),
		fmt.Sprintf("GITHUB_BRANCH=%s", d.branchName()),
	)
	err := cmd.Run()
	if err != nil {
		err = errors.Wrapf(err, "stdout: %s\nstderr: %s", stdout.String(), stderr.String())
	}
	return err
}

func (d *dependabotPullRequest) sendUpstream() error {
	if d.dryrun {
		return nil
	}
	return nil
}

func (d *dependabotPullRequest) currentStatus() (prStatus, error) {
	// Check whether the PR is already opened in the upstream repository.
	if exists, err := d.getUpstreamPR(); err != nil {
		return unknownStatus, err
	} else if exists != "" {
		return upstreamStatus, nil
	}

	// Check whether the PR has conflicts.
	ok, err := d.isMergeable()
	if err != nil {
		return unknownStatus, err
	}
	if !ok {
		return notMergeableStatus, nil
	}
	if d.isLabelPresent(rebaseLabel) {
		return mergeableStatus, nil
	}

	// Check whether the CI status is ok.
	checkStatus, err := d.checkStatus()
	if err != nil {
		return unknownStatus, err
	}
	switch checkStatus {
	case "failure":
		return failedCheckStatus, nil
	case "pending":
		return pendingCheckStatus, nil
	case "success":
		return okCheckStatus, nil
	}
	return missingCheckStatus, nil
}

func (d *dependabotPullRequest) String() string {
	return fmt.Sprintf("%s @ %s", d.repoName(), d.branchName())
}
