// Utility program to bump the Go versions in https://github.com/prometheus/golang-builder.
// Only works for patch updates, not minor neither major.
package main

import (
	"bufio"
	"flag"
	"fmt"
	"io/ioutil"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

var (
	help      bool
	versionRe *regexp.Regexp
)

func init() {
	flag.BoolVar(&help, "help", false, "Help message")
	versionRe = regexp.MustCompile(`^(?P<MINOR>\d\.\d+)(?:\.\d+)?$`)
}

func versionURL(v string) string {
	return fmt.Sprintf("https://dl.google.com/go/go%s.linux-amd64.tar.gz", v)
}

func sha256URL(v string) string {
	return versionURL(v) + ".sha256"
}

func getSHA256(v string) (string, error) {
	resp, err := http.Get(sha256URL(v))
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	b, err := ioutil.ReadAll(resp.Body)
	if err != nil {
		return "", err
	}
	return string(b), nil
}

func getLastVersion(v string) (string, error) {
	last := v
	for i := 1; ; i++ {
		next := fmt.Sprintf("%s.%d", v, i)

		resp, err := http.Head(versionURL(next))
		if err != nil {
			return "", err
		}
		defer resp.Body.Close()

		if resp.StatusCode%100 != 0 {
			return last, nil
		}
		last = next
	}
}

func getExactVersion(v string) (string, error) {
	re := regexp.MustCompile(fmt.Sprintf(`VERSION\s+:=\s+(%s.\d+)`, v))
	f, err := os.Open(filepath.Join(v, "Makefile.COMMON"))
	if err != nil {
		return "", err
	}
	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		m := re.FindSubmatch(scanner.Bytes())
		if m != nil {
			return string(m[1]), nil
		}
	}
	if err := scanner.Err(); err != nil {
		return "", err
	}
	return "", fmt.Errorf("couldn't get exact version for %s", v)
}

func bumpVersion(current string) (string, error) {
	m := versionRe.FindSubmatch([]byte(current))
	if m == nil {
		panic(fmt.Sprintf("can't find base for version %s", current))
	}
	base := string(m[1])
	next, err := getLastVersion(base)
	if err != nil {
		return "", err
	}
	if current == next {
		fmt.Printf("> no new version for %s\n", current)
		return "", nil
	}

	currentSHA, err := getSHA256(current)
	if err != nil {
		return "", err
	}
	nextSHA, err := getSHA256(next)
	if err != nil {
		return "", err
	}

	for _, f := range []string{"../README.md", "Makefile.COMMON", "base/Dockerfile"} {
		f = filepath.Join(base, f)
		b, err := ioutil.ReadFile(f)
		if err != nil {
			return "", err
		}
		out := string(b)
		out = strings.ReplaceAll(out, current, next)
		out = strings.ReplaceAll(out, currentSHA, nextSHA)
		if err := ioutil.WriteFile(f, []byte(out), 0644); err != nil {
			return "", err
		}
	}
	fmt.Printf("> updated from %s to %s\n", current, next)
	return next, nil
}

func main() {
	flag.Parse()
	if help {
		fmt.Fprintln(os.Stderr, "Bump patch Go versions in github.com/prometheus/golang-builder.")
		flag.PrintDefaults()
		os.Exit(0)
	}

	versions := make([]string, 0)
	files, err := ioutil.ReadDir(".")
	if err != nil {
		panic(err)
	}
	for _, f := range files {
		if !f.IsDir() {
			continue
		}
		if !versionRe.Match([]byte(f.Name())) {
			continue
		}
		versions = append(versions, f.Name())
	}

	if len(versions) == 0 {
		fmt.Println("Couldn't find any Go version in the current directory.")
		return
	}

	nexts := make([]string, 0)
	for _, v := range versions {
		fmt.Printf("processing %s\n", v)
		v, err := getExactVersion(v)
		if err != nil {
			fmt.Printf("failed to detect current version %v: %v\n", v, err)
		}
		next, err := bumpVersion(v)
		if err != nil {
			fmt.Printf("failed to bump version %v: %v\n", v, err)
		}
		if next != "" {
			nexts = append(nexts, next)
		}
	}

	if len(nexts) != 0 {
		fmt.Printf("\nRun the following command to commit the changes:\n\n")
		fmt.Printf("git commit . --no-edit --message \"Bump to Go %s\"\n", strings.Join(nexts, " and "))
	}
}
