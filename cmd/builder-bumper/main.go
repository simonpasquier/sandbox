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
	"sort"
	"strconv"
	"strings"

	"github.com/pkg/errors"
)

var (
	help      bool
	versionRe *regexp.Regexp
)

func init() {
	flag.BoolVar(&help, "help", false, "Help message")
	versionRe = regexp.MustCompile(`^(?:\d\.(\d+))(?:\.(\d+))?$`)
}

type goVersion struct {
	major int
	minor int
}

func newGoVersion(v string) *goVersion {
	m := versionRe.FindSubmatch([]byte(v))
	if len(m) != 3 {
		return nil
	}
	major, err := strconv.Atoi(string(m[1]))
	if err != nil {
		panic(err)
	}
	minor, err := strconv.Atoi(string(m[2]))
	if err != nil {
		panic(err)
	}
	return &goVersion{
		major: major,
		minor: minor,
	}
}

func (g *goVersion) Major() string {
	return fmt.Sprintf("1.%d", g.major)
}

func (g *goVersion) GolangVersion() string {
	if g.minor == 0 {
		return g.Major()
	}
	return g.String()
}

func (g *goVersion) String() string {
	return fmt.Sprintf("1.%d.%d", g.major, g.minor)
}

func (g *goVersion) Less(o *goVersion) bool {
	if g.major == o.major {
		return g.minor < o.minor
	}
	return g.major < o.major
}

func (g *goVersion) Equal(o *goVersion) bool {
	return g.major == o.major && g.minor == o.minor
}

// url returns the URL to download the Go version.
func (g *goVersion) url() string {
	return fmt.Sprintf("https://dl.google.com/go/go%s.linux-amd64.tar.gz", g.GolangVersion())
}

// getSHA256 returns the SHA256 of the Go version.
func (g *goVersion) getSHA256() (string, error) {
	resp, err := http.Get(g.url() + ".sha256")
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	b, err := ioutil.ReadAll(resp.Body)
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(b)), nil
}

func (g *goVersion) getLastVersion() (*goVersion, error) {
	last := *g
	for {
		next := last
		next.minor++
		resp, err := http.Head(next.url())
		if err != nil {
			return nil, err
		}
		defer resp.Body.Close()

		if resp.StatusCode/100 != 2 {
			return &last, nil
		}
		last = next
	}
}

func getExactVersionFromDir(d string) (*goVersion, error) {
	re := regexp.MustCompile(fmt.Sprintf(`^\s*VERSION\s*:=\s*(%s.\d+)`, d))
	f, err := os.Open(filepath.Join(d, "Makefile.COMMON"))
	if err != nil {
		return nil, err
	}
	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		m := re.FindSubmatch(scanner.Bytes())
		if m != nil {
			return newGoVersion(string(m[1])), nil
		}
	}
	if err := scanner.Err(); err != nil {
		return nil, err
	}
	return nil, fmt.Errorf("couldn't get exact version for %s", d)
}

func replace(filename string, replacers []func(string) (string, error)) error {
	b, err := ioutil.ReadFile(filename)
	if err != nil {
		return err
	}
	out := string(b)
	for _, fn := range replacers {
		out, err = fn(out)
		if err != nil {
			return err
		}
	}
	return ioutil.WriteFile(filename, []byte(out), 0644)
}

func getNextMajor(v string) *goVersion {
	version := newGoVersion(v + ".0")
	if version == nil {
		return nil
	}
	version.major++
	resp, err := http.Head(version.url())
	if err != nil {
		return nil
	}
	defer resp.Body.Close()
	if resp.StatusCode/100 != 2 {
		return nil
	}
	return version
}

func shaReplacer(old, new *goVersion) func(string) (string, error) {
	oldSHA, err := old.getSHA256()
	if err != nil {
		return func(string) (string, error) { return "", err }
	}
	nextSHA, err := new.getSHA256()
	if err != nil {
		return func(string) (string, error) { return "", err }
	}

	return func(out string) (string, error) {
		return strings.ReplaceAll(out, oldSHA, nextSHA), nil
	}
}

func majorVersionReplacer(old, new *goVersion) func(string) (string, error) {
	return func(out string) (string, error) {
		return strings.ReplaceAll(out, old.Major(), new.Major()), nil
	}
}

func golangVersionReplacer(old, new *goVersion) func(string) (string, error) {
	return func(out string) (string, error) {
		return strings.ReplaceAll(out, old.GolangVersion(), new.GolangVersion()), nil
	}
}

func fullVersionReplacer(old, new *goVersion) func(string) (string, error) {
	return func(out string) (string, error) {
		return strings.ReplaceAll(out, old.String(), new.String()), nil
	}
}

func replaceMajor(old, current, next *goVersion) error {
	err := filepath.Walk(old.Major(), func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if info.IsDir() {
			return nil
		}
		if info.Name() == "Makefile.COMMON" {
			return replace(path,
				[]func(string) (string, error){
					fullVersionReplacer(old, next),
				},
			)
		}
		return replace(path,
			[]func(string) (string, error){
				golangVersionReplacer(old, next),
				majorVersionReplacer(old, next),
				shaReplacer(old, next),
			},
		)
	})
	if err != nil {
		return err
	}

	if err := os.Rename(old.Major(), next.Major()); err != nil {
		return errors.Wrap(err, "failed to create new version directory")
	}

	// Update README.md
	err = replace("Makefile",
		[]func(string) (string, error){
			majorVersionReplacer(current, next),
			majorVersionReplacer(old, current),
		},
	)
	if err != nil {
		return nil
	}

	// Update README.md
	return replace("README.md",
		[]func(string) (string, error){
			fullVersionReplacer(current, next),
			majorVersionReplacer(current, next),
			majorVersionReplacer(old, current),
			fullVersionReplacer(old, current),
		},
	)
}

func updateNextMinor(dir string) (*goVersion, error) {
	fmt.Printf("> processing %s\n", dir)

	current, err := getExactVersionFromDir(dir)
	if err != nil {
		return nil, errors.Wrapf(err, "failed to detect current version of %s", dir)
	}

	next, err := current.getLastVersion()
	if err != nil || next.Equal(current) {
		if err == nil {
			fmt.Printf("> no version change for Go %s\n", next.GolangVersion())
		}
		return nil, err
	}

	err = replace(filepath.Join(current.Major(), "base/Dockerfile"),
		[]func(string) (string, error){
			golangVersionReplacer(current, next),
			shaReplacer(current, next),
		},
	)
	if err != nil {
		return nil, err
	}

	err = replace(filepath.Join(current.Major(), "Makefile.COMMON"),
		[]func(string) (string, error){
			fullVersionReplacer(current, next),
		},
	)
	if err != nil {
		return nil, err
	}

	err = replace(filepath.Join("README.md"),
		[]func(string) (string, error){
			fullVersionReplacer(current, next),
		},
	)
	if err != nil {
		return nil, err
	}

	fmt.Printf("> updated from %s to %s\n", current, next)
	return next, nil
}

func main() {
	flag.Parse()
	if help {
		fmt.Fprintln(os.Stderr, "Bump Go versions in github.com/prometheus/golang-builder.")
		flag.PrintDefaults()
		os.Exit(0)
	}
	if err := run(); err != nil {
		fmt.Println(err)
		os.Exit(1)
	}
}

func run() error {
	dirs := make([]string, 0)
	files, err := ioutil.ReadDir(".")
	if err != nil {
		return err
	}
	for _, f := range files {
		if !f.IsDir() {
			continue
		}
		if !versionRe.Match([]byte(f.Name())) {
			continue
		}
		dirs = append(dirs, f.Name())
	}

	if len(dirs) != 2 {
		return errors.Errorf("Expected 2 versions of Go but got %d\n", len(dirs))
	}

	// check if a new major version exists.
	nexts := make([]*goVersion, 0)
	if next := getNextMajor(dirs[1]); next != nil {
		fmt.Println("> found new major version of Go!")
		old, err := getExactVersionFromDir(dirs[0])
		if err != nil {
			return err
		}
		current, err := getExactVersionFromDir(dirs[1])
		if err != nil {
			return err
		}
		if err = replaceMajor(old, current, next); err != nil {
			return err
		}
		nexts = append(nexts, next)
	} else {
		for _, d := range dirs {
			next, err := updateNextMinor(d)
			if err != nil {
				return err
			}
			if next != nil {
				nexts = append(nexts, next)
			}
		}
	}

	if len(nexts) != 0 {
		sort.SliceStable(nexts, func(i, j int) bool {
			return nexts[i].Less(nexts[j])
		})
		fmt.Printf("\nRun the following command to commit the changes:\n\n")
		vs := make([]string, 0)
		for _, v := range nexts {
			vs = append(vs, v.String())
		}
		fmt.Printf("git checkout -b golang-%s\n", strings.Join(vs, "-"))
		fmt.Printf("git commit . --no-edit --message \"Bump to Go %s\"\n", strings.Join(vs, " and "))
	}

	return nil
}
