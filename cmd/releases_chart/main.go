package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"io/ioutil"
	"net/http"
	"os"
	"sort"
	"strings"
)

const (
	githubTagAPI    = "https://api.github.com/repos/%s/%s/releases/tags/%s"
	githubLatestAPI = "https://api.github.com/repos/%s/%s/releases/latest"
)

var (
	help           bool
	org, repo, tag string
	excludeExts    string
)

func init() {
	flag.BoolVar(&help, "help", false, "Show help")
	flag.StringVar(&org, "org", "prometheus", "GitHub organization")
	flag.StringVar(&repo, "repository", "prometheus", "GitHub repository")
	flag.StringVar(&tag, "tag", "", "Git tag")
	flag.StringVar(&excludeExts, "exclude", "txt", "List of comma-separated extensions to exclude")
}

type asset struct {
	Name          string `json:"name"`
	DownloadCount int    `json:"download_count"`
}

type release struct {
	TagName string  `json:"tag_name"`
	Assets  []asset `json:"assets"`
}

func main() {
	flag.Parse()
	if help {
		fmt.Fprintln(os.Stderr, "Display release assets by popularity")
		flag.PrintDefaults()
		return
	}
	exts := strings.Split(excludeExts, ",")

	var url string
	if tag == "" {
		url = fmt.Sprintf(githubLatestAPI, org, repo)
	} else {
		url = fmt.Sprintf(githubTagAPI, org, repo, tag)

	}
	res, err := http.Get(url)
	if err != nil {
		panic(err)
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		panic(fmt.Sprintf("got %d status code", res.StatusCode))
	}

	b, err := ioutil.ReadAll(res.Body)
	if err != nil {
		panic(err)
	}

	var rel release
	err = json.Unmarshal(b, &rel)
	if err != nil {
		panic(err)
	}

	sort.Slice(rel.Assets, func(i, j int) bool {
		return rel.Assets[i].DownloadCount > rel.Assets[j].DownloadCount
	})

	assets := []asset{}
	for _, a := range rel.Assets {
		var discard bool
		for _, ext := range exts {
			discard = discard || strings.HasSuffix(a.Name, ext)
		}
		if discard {
			continue
		}
		assets = append(assets, a)
	}

	fmt.Println("Statistics for release", rel.TagName)
	for i, a := range assets {
		fmt.Printf("%2d) %-64s%d\n", i+1, a.Name, a.DownloadCount)
	}
}
