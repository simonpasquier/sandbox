package main

import (
	"bytes"
	"context"
	"flag"
	"fmt"
	"io/ioutil"
	"log"
	"os"
	"regexp"
	"strconv"
	"strings"
	"text/template"
	"time"

	"github.com/google/go-github/v25/github"
	"github.com/jszwedko/go-circleci"
	"github.com/pkg/errors"
	"golang.org/x/oauth2"
)

var (
	help             bool
	ghtPath, cctPath string
	org              string
	repoNames        string
)

func init() {
	flag.BoolVar(&help, "help", false, "Show help")
	flag.StringVar(&cctPath, "circleci.token", "", "Path to your Circle CI token")
	flag.StringVar(&ghtPath, "github.token", "", "Path to your GitHub token")
	flag.StringVar(&org, "github.organization", "prometheus", "Name of the GitHub organization")
	flag.StringVar(&repoNames, "github.repositories", "", "Comma-separated list of the repositories to filter on (default: all)")
}

type travisSettings struct {
	Enabled bool
}

type circleSettings struct {
	Enabled bool
	Flags   circleci.FeatureFlags
}

type integration struct {
	Active    bool
	CreatedAt time.Time
	UpdatedAt time.Time
	Events    []string
	ID        string
	URL       string
}

//TODO: support additional fields once supported by the GitHub API client.
type key struct {
	//CreatedAt time.Time
	Name string
	//Verified bool
	ReadOnly bool
}

type repository struct {
	Name         string
	Integrations []integration
	Keys         []key
	Circle       circleSettings
	Travis       travisSettings
}

const repoTmpl = `Repository: {{ .Name }}

Integrations:{{ range .Integrations }}
- ID: {{ .ID }}
  Active: {{ if .Active }}yes{{ else }}no{{ end }}
  URL: {{ .URL }}
  Events: {{ join .Events "," }}
  Created at: {{ .CreatedAt }}
  Updated at: {{ .UpdatedAt }}
{{- end }}

Keys:{{ range .Keys }}
- {{ .Name }} ({{ if .ReadOnly }}read-only{{ else }}read-write{{ end }})
{{- else }} none{{- end }}

CircleCI:{{ if .Circle.Enabled }}
- OSS: {{ .Circle.Flags.OSS }}
  Build fork PRs: {{ .Circle.Flags.BuildForkPRs }}
{{ else }} not enabled{{ end }}

TravisCI:{{ if .Travis.Enabled }}
-
{{ else }} not enabled{{ end }}`

func (r *repository) String() string {
	t := template.New("repository").Funcs(template.FuncMap{"join": strings.Join})
	t, err := t.Parse(repoTmpl)
	if err != nil {
		return err.Error()
	}
	b := bytes.Buffer{}
	err = t.Execute(&b, r)
	if err != nil {
		return err.Error()
	}
	return b.String()
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
	repos, err := run(org, ghtPath, cctPath)
	if err != nil {
		log.Fatal(err)
	}
	for _, repo := range repos {
		fmt.Println(repo.String())
	}
}

func run(org string, ghToken string, ccToken string) ([]repository, error) {
	ght, err := readTokenFile(ghToken)
	if err != nil {
		return nil, err
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
			return nil, err
		}
		ccc = &circleci.Client{Token: cct}
	}

	var ghRepos = make([]*github.Repository, 0)
	opt := &github.RepositoryListByOrgOptions{
		ListOptions: github.ListOptions{PerPage: 10},
	}
	for {
		r, resp, err := ghc.Repositories.ListByOrg(ctx, org, opt)
		if err != nil {
			return nil, errors.Wrap(err, "Fail to list GitHub repositories")
		}
		if repoNames != "" {
			for _, r := range r {
				re := regexp.MustCompile("(?:^|,)" + r.GetName() + "(,|$)")
				if re.MatchString(repoNames) {
					ghRepos = append(ghRepos, r)
				}
			}
		} else {
			ghRepos = append(ghRepos, r...)
		}
		if resp.NextPage == 0 {
			break
		}
		opt.Page = resp.NextPage
	}

	cc := make(map[string]circleci.FeatureFlags)
	if ccc != nil {
		ccp, err := ccc.ListProjects()
		if err != nil {
			return nil, errors.Wrap(err, "fail to list CircleCI projects")
		}
		for _, p := range ccp {
			if org == p.Username {
				cc[p.Reponame] = p.FeatureFlags
			}
		}
	}

	repos := make([]repository, 0, len(ghRepos))
	for _, r := range ghRepos {
		repo := repository{Name: r.GetName()}

		hooks, _, err := ghc.Repositories.ListHooks(ctx, org, r.GetName(), nil)
		if err != nil {
			return nil, errors.Wrap(err, fmt.Sprintf("%s: fail to list hooks", r.GetName()))
		}
		repo.Integrations = make([]integration, 0, len(hooks))
		for _, h := range hooks {
			var u string
			if _, ok := h.Config["url"]; ok {
				u, _ = h.Config["url"].(string)
			}
			repo.Integrations = append(repo.Integrations, integration{
				ID:        strconv.FormatInt(h.GetID(), 10),
				Active:    h.GetActive(),
				Events:    h.Events,
				CreatedAt: h.GetCreatedAt(),
				UpdatedAt: h.GetUpdatedAt(),
				URL:       u,
			})
		}

		//TODO: list branch settings.
		branches, _, err := ghc.Repositories.ListBranches(ctx, org, r.GetName(), nil)

		//TODO: list security alerts.
		//TODO: list installed apps.
		keys, _, err := ghc.Repositories.ListKeys(ctx, org, r.GetName(), nil)
		repo.Keys = make([]key, 0, len(keys))
		for _, k := range keys {
			repo.Keys = append(repo.Keys, key{
				Name:     k.GetTitle(),
				ReadOnly: k.GetReadOnly(),
			})
		}

		if len(cc) > 0 {
			ccFlags, ok := cc[r.GetName()]
			if ok {
				repo.Circle = circleSettings{
					Enabled: true,
					Flags:   ccFlags,
				}
			}
		}

		repos = append(repos, repo)
	}
	return repos, nil
}
