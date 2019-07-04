// Utility program to generate changelog entries for Prometheus projects.
package main

import (
	"context"
	"flag"
	"fmt"
	"io/ioutil"
	"log"
	"os"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"text/template"
	"time"

	"github.com/google/go-github/v26/github"
	"github.com/pkg/errors"
	"golang.org/x/oauth2"
)

var (
	labelPrefix = "changelog/"
	skipLabel   = labelPrefix + "skip"

	help     bool
	ghtPath  string
	last     string
	org      string
	repoName string
	rc       bool

	tagRe = regexp.MustCompile(`^v(\d+)\.(\d+)\.(\d+)$`)
)

func init() {
	flag.BoolVar(&help, "help", false, "Show help")
	flag.StringVar(&ghtPath, "github.token", "", "Path to your GitHub token")
	flag.StringVar(&org, "github.organization", "prometheus", "Name of the GitHub organization")
	flag.StringVar(&repoName, "github.repository", "prometheus", "Name of the repository")
	flag.StringVar(&last, "tag", "", "The last tag to check against")
	flag.BoolVar(&rc, "rc", true, "")
}

type pullRequest struct {
	Number int
	Title  string
	Kind   string
}

// pullRequests is a sortable slice of pullRequest.
type pullRequests []pullRequest

func (p pullRequests) Len() int { return len(p) }

// Less sorts pull requests by their kind.
// The order is CHANGE first then FEATURE then ENHANCEMENT then BUGFIX.
func (p pullRequests) Less(i, j int) bool {
	if p[i].Kind == p[j].Kind {
		return true
	}
	if p[j].Kind == "" {
		return true
	}
	switch p[i].Kind {
	case "CHANGE":
		return true
	case "FEATURE":
		return p[j].Kind != "CHANGE"
	case "ENHANCEMENT":
		return p[j].Kind != "CHANGE" && p[j].Kind != "FEATURE"
	}
	return false
}

func (p pullRequests) Swap(i, j int) { p[i], p[j] = p[j], p[i] }

type changelogData struct {
	Version      string
	Date         string
	PullRequests pullRequests
	Skipped      pullRequests
}

const changelogTmpl = `## {{ .Version }} / {{ .Date }}
{{ range .PullRequests }}
* [{{ .Kind }}] {{ .Title }}. #{{ .Number }}
{{- end }}
<!-- Skipped pull requests:{{ range .Skipped }}
* [{{ .Kind }}] {{ .Title }}. #{{ .Number }}
{{- end }} -->
`

func readAll(list func(*github.ListOptions) (*github.Response, error)) error {
	opt := github.ListOptions{PerPage: 10}
	for {
		resp, err := list(&opt)
		if err != nil {
			return err
		}
		if resp == nil || resp.NextPage == 0 {
			return nil
		}
		opt.Page = resp.NextPage
	}
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
		fmt.Fprintln(os.Stderr, "Generate a changelog entry")
		flag.PrintDefaults()
		os.Exit(0)
	}
	if ghtPath == "" {
		log.Fatal("github.token parameters is mandataory")
	}
	err := run(org, repoName, last, rc, ghtPath)
	if err != nil {
		log.Fatal(err)
	}
}

func run(org string, repoName string, lastTag string, rc bool, ghToken string) error {
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

	// Get the datetime of the last version tag.
	if lastTag == "" {
		err = readAll(
			func(opts *github.ListOptions) (*github.Response, error) {
				tags, resp, err := ghc.Repositories.ListTags(ctx, org, repoName, &github.ListOptions{})
				if err != nil {
					return nil, errors.Wrap(err, "Fail to get the latest GitHub release")
				}
				// Tags are listed by most recents first.
				for _, tag := range tags {
					if strings.HasSuffix(tag.GetName(), ".0") {
						lastTag = tag.GetName()
						return nil, nil
					}
				}
				return resp, nil
			},
		)
		if err != nil {
			return err
		}
	}
	m := tagRe.FindStringSubmatch(lastTag)
	if m == nil || len(m) != 4 {
		return errors.Errorf("%s is not a valid tag", lastTag)
	}
	next, err := strconv.Atoi(m[2])
	if err != nil {
		return errors.Wrap(err, fmt.Sprintf("%s is not a valid tag", lastTag))
	}
	nextVersion := fmt.Sprintf("%s.%d.%s", m[1], next+1, m[3])
	if rc {
		nextVersion += "-rc.0"
	}

	commit, _, err := ghc.Repositories.GetCommit(ctx, org, repoName, lastTag)
	if err != nil {
		return errors.Wrap(err, fmt.Sprintf("Fail to get the GitHub commit for %s", lastTag))
	}
	lastTagTime := commit.GetCommit().GetCommitter().GetDate()
	lastCommitSHA := commit.GetSHA()

	// Gather all pull requests merged since the last tag.
	var (
		prs     pullRequests
		skipped pullRequests
	)
	err = readAll(
		func(opts *github.ListOptions) (*github.Response, error) {
			ghPrs, resp, err := ghc.PullRequests.List(ctx, org, repoName, &github.PullRequestListOptions{
				State:       "closed",
				Sort:        "updated",
				Direction:   "desc",
				ListOptions: *opts,
			})
			if err != nil {
				return nil, errors.Wrap(err, "Fail to list GitHub pull requests")
			}
			for _, pr := range ghPrs {
				if pr.GetBase().GetRef() != "master" {
					continue
				}
				if pr.GetUpdatedAt().Before(lastTagTime) {
					// We've reached pull requests that haven't changed since the reference tag so we can stop.
					return nil, nil
				}
				if pr.GetMergedAt().IsZero() || pr.GetMergedAt().Before(lastTagTime) {
					continue
				}
				if pr.GetMergeCommitSHA() == lastCommitSHA {
					continue
				}

				var (
					kind []string
					skip bool
				)
				for _, lbl := range pr.Labels {
					if lbl.GetName() == skipLabel {
						skip = true
					}
					if strings.HasPrefix(lbl.GetName(), labelPrefix) {
						kind = append(kind, strings.ToUpper(strings.TrimPrefix(lbl.GetName(), labelPrefix)))
					}
				}
				p := pullRequest{
					Kind:   strings.Join(kind, "/"),
					Title:  pr.GetTitle(),
					Number: pr.GetNumber(),
				}
				if skip {
					skipped = append(skipped, p)
				} else {
					prs = append(prs, p)
				}
			}
			return resp, nil
		},
	)
	if err != nil {
		return err
	}

	tmpl, err := template.New("changelog").Parse(changelogTmpl)
	if err != nil {
		return errors.Wrap(err, "invalid template")
	}

	sort.Stable(prs)
	sort.Stable(skipped)
	return tmpl.Execute(os.Stdout, &changelogData{
		Version:      nextVersion,
		Date:         time.Now().Format("2006-01-02"),
		PullRequests: prs,
		Skipped:      skipped,
	})
}
