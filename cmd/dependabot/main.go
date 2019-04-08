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
	//TODO: replace with kingpin
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
				s := pr.getState()
				for {
					next, err := process(pr, s)
					if err != nil {
						fmt.Printf("✗ %s: failed processing %q state: %s\n", pr, s, err)
						break
					}

					if s == next {
						err := pr.setState(next)
						if err != nil {
							fmt.Printf("✗ %s: failed setting state to %q: %s\n", pr, s, err)
						}
						fmt.Printf("✔ %s: new state %q\n", pr, s)
						break
					}
					s = next
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
						pr := newDependabotPullRequest(
							ctx,
							ghc,
							pr,
							ghUpstreamOrg,
							updateScript,
							dryrun,
						)
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

type dependabotPullRequest struct {
	ctx    context.Context
	c      *github.Client
	pr     *github.PullRequest
	labels []string
	// The name of the upstream repository.
	upstream     string
	updateScript string
	updatedOnce  bool
	dryrun       bool
}

func newDependabotPullRequest(
	ctx context.Context,
	c *github.Client,
	pr *github.PullRequest,
	upstream string,
	updateScript string,
	dryrun bool,
) *dependabotPullRequest {
	labels := make([]string, 0, len(pr.Labels))
	for _, l := range pr.Labels {
		labels = append(labels, l.GetName())
	}
	return &dependabotPullRequest{
		ctx:          ctx,
		c:            c,
		pr:           pr,
		labels:       labels,
		upstream:     upstream,
		updateScript: updateScript,
		dryrun:       dryrun,
	}
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
	return d.pr.Head.GetRef()
	//return d.pr.Head.GetSHA()
}

// getUpstreamPR returns the URL string of the upstream pull request and a
// boolean flag indicating whether or not the request is open.
func (d *dependabotPullRequest) getUpstreamPR() (string, bool, error) {
	var open bool
	// Search closed requests first.
	opt := github.PullRequestListOptions{Head: d.pr.Head.GetLabel(), State: "closed"}
	prs, _, err := d.c.PullRequests.List(d.ctx, d.upstream, d.repoName(), &opt)
	if err != nil {
		return "", open, err
	}
	for _, pr := range prs {
		merged, _, err := d.c.PullRequests.IsMerged(d.ctx, d.upstream, d.repoName(), pr.GetNumber())
		if err != nil {
			return "", open, err
		}
		if merged {
			return pr.GetURL(), open, nil
		}
	}

	// Search open requests.
	open = true
	opt.State = "open"
	prs, _, err = d.c.PullRequests.List(d.ctx, d.upstream, d.repoName(), &opt)
	if err != nil {
		return "", open, err
	}
	if len(prs) == 1 {
		return prs[0].GetURL(), open, nil
	}
	return "", open, nil
}

// isMergeable returns true iff the pull request can be merged (eg no conflicts).
func (d *dependabotPullRequest) isMergeable() (bool, error) {
	var i = 0
	// TODO: make it more generic.
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
	statePrefixLabel = "state/"

	unknownState = "unknown"

	needUpdateState   = "need update"
	okUpdateState     = "update ok"
	failedUpdateState = "update failed"

	needRebaseState    = "need rebase"
	pendingRebaseState = "pending rebase"
	okRebaseState      = "rebase ok"

	missingChecksState = "checks missing"
	failedChecksState  = "checks failed"
	pendingChecksState = "checks pending"
	okChecksState      = "checks ok"

	submittedUpstreamState = "upstream submitted"
)

func process(d *dependabotPullRequest, state string) (string, error) {
	checkStatusToState := func() (string, error) {
		s, err := d.checkStatus()
		if err != nil {
			return "", err
		}
		switch s {
		case "":
			return missingChecksState, nil
		case "failure":
			return failedChecksState, nil
		case "pending":
			return pendingChecksState, nil
		case "success":
			return okChecksState, nil
		}
		return "", errors.Errorf("unknown check status: %s", s)
	}

	switch state {
	case unknownState:
		return pendingRebaseState, nil

	case needUpdateState:
		err := d.runScript()
		if err != nil {
			return failedUpdateState, nil
		}
		return okUpdateState, nil
	case okUpdateState:
		return checkStatusToState()

	case needRebaseState:
		err := d.recreate()
		if err != nil {
			return "", err
		}
		return pendingRebaseState, nil
	case pendingRebaseState:
		ok, err := d.isMergeable()
		if err != nil {
			return "", err
		}
		if ok {
			return okRebaseState, nil
		}
	case okRebaseState:
		return checkStatusToState()

	case missingChecksState:
		if recreateMissing {
			return needRebaseState, nil
		}
	case failedChecksState:
		if !d.updatedOnce {
			return needUpdateState, nil
		}
	case pendingChecksState:
		return checkStatusToState()
	case okChecksState:
		err := d.sendUpstream()
		if err != nil {
			return "", err
		}
		return submittedUpstreamState, nil
		//TODO: check upstream for conflicts.
	}

	// No state change.
	return state, nil
}

// getState returns the state of the pull request.
func (d *dependabotPullRequest) getState() string {
	for _, l := range d.getLabels() {
		s := strings.TrimPrefix(l, statePrefixLabel)
		if s != l {
			return s
		}
	}
	return unknownState
}

// setState records the state of the pull request.
func (d *dependabotPullRequest) setState(s string) error {
	var found bool
	s = statePrefixLabel + s
	labels := d.getLabels()
	for i := range labels {
		found = strings.HasPrefix(labels[i], statePrefixLabel)
		if !found {
			continue
		}
		if labels[i] == s {
			return nil
		}
		labels[i] = s
		break
	}
	if !found {
		labels = append(labels, s)
	}
	return d.updateLabels(labels)
}

func (d *dependabotPullRequest) getLabels() []string {
	return d.labels
}

func (d *dependabotPullRequest) updateLabels(labels []string) error {
	var err error
	update := &github.IssueRequest{
		Labels: &labels,
	}
	if !d.dryrun {
		_, _, err = d.c.Issues.Edit(d.ctx, d.userName(), d.repoName(), d.pr.GetNumber(), update)
	}
	if err == nil {
		d.labels = labels
	}
	return err
}

// recreate asks dependabot to recreate the pull request.
func (d *dependabotPullRequest) recreate() error {
	if d.dryrun {
		return nil
	}
	recreateCommand := "@dependabot recreate"
	comment := github.IssueComment{
		Body: &recreateCommand,
	}
	_, _, err := d.c.Issues.CreateComment(d.ctx, d.userName(), d.repoName(), d.pr.GetNumber(), &comment)
	return err
}

// checkStatus returns the CI status of the pull request (failure, pending, success or empty string).
func (d *dependabotPullRequest) checkStatus() (string, error) {
	fmt.Println("check", d.sha())
	status, _, err := d.c.Repositories.GetCombinedStatus(d.ctx, d.userName(), d.repoName(), d.sha(), nil)
	if err != nil {
		return "", err
	}
	if status.GetTotalCount() == 0 {
		return "", nil
	}
	return status.GetState(), nil
}

// runScript runs the configured script on the pull request.
// It shouldn't be called more than once.
func (d *dependabotPullRequest) runScript() error {
	if d.updatedOnce {
		return errors.New("script already executed once")
	}
	d.updatedOnce = true
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

// sendUpstream opens the pull request to the upstream repository.
func (d *dependabotPullRequest) sendUpstream() error {
	if d.dryrun {
		return nil
	}
	if _, ok, err := d.getUpstreamPR(); err != nil {
		return err
	} else if ok {
		return nil
	}
	return nil
}

func (d *dependabotPullRequest) String() string {
	return fmt.Sprintf("%s @ %s", d.repoName(), d.branchName())
}
