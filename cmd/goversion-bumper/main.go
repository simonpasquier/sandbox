// Utility program to bump the major Go version in Prometheus projects.
package main

import (
	"flag"
	"fmt"
	"io/ioutil"
	"os"
	"path/filepath"
	"regexp"

	"github.com/pkg/errors"
	"gopkg.in/yaml.v2"
)

var (
	help          bool
	version, path string
	versionRe     *regexp.Regexp
)

func init() {
	flag.BoolVar(&help, "help", false, "Help message")
	flag.StringVar(&version, "version", "", "Go version")
	flag.StringVar(&path, "path", ".", "Repository path")

	versionRe = regexp.MustCompile(`^(\d)\.(\d+)$`)
}

func main() {
	flag.Parse()
	if help {
		fmt.Fprintln(os.Stderr, "Bump Go version for a github.com/prometheus project.")
		flag.PrintDefaults()
		os.Exit(0)
	}
	if len(version) == 0 {
		flag.PrintDefaults()
		os.Exit(1)
	}
	errs := run(version, path)
	if len(errs) > 0 {
		for _, err := range errs {
			fmt.Println("✗ got error:", err)
		}
		os.Exit(1)
	}
}

func processFile(filename string, process func(string) (string, error)) error {
	f, err := os.Open(filename)
	if err != nil {
		return err
	}
	stat, err := f.Stat()
	if err != nil {
		return err
	}
	mod := stat.Mode()
	f.Close()

	b, err := ioutil.ReadFile(filename)
	if err != nil {
		return err
	}
	in := string(b)
	out, err := process(in)
	if err != nil {
		return err
	}

	if in == out {
		return nil
	}

	return ioutil.WriteFile(filename, []byte(out), mod)
}

const promuVersion = `go:
    # Whenever the Go version is updated here, .travis.yml and
    # .circle/config.yml should also be updated.
    version: %s
`

func processPromu(s string) (string, error) {
	type promuConfig struct {
		Go map[string]string `yaml:"go"`
	}
	cfg := &promuConfig{}
	err := yaml.Unmarshal([]byte(s), cfg)
	if err != nil {
		return "", err
	}

	if _, ok := cfg.Go["version"]; !ok {
		return fmt.Sprintf(promuVersion, version) + s, nil
	}

	promuRe := regexp.MustCompile(`(?m:^(\s*version:\s*)(\d\.\d+)$)`)
	return promuRe.ReplaceAllString(s, "${1}"+version), nil
}

func processCircle(s string) (string, error) {
	circleRe := regexp.MustCompile(`(?m:^(.*golang(?:[^:])?:)(?:\d\.\d+)([^\d]+)?$)`)
	return circleRe.ReplaceAllString(s, "${1}"+version+"${2}"), nil
}

func processTravis(s string) (string, error) {
	type travisConfig struct {
		Go []string `yaml:"go"`
	}
	cfg := &travisConfig{}
	err := yaml.Unmarshal([]byte(s), cfg)
	if err != nil {
		return "", err
	}

	if len(cfg.Go) == 0 {
		return "", errors.New("couldn't find go version in .travis.yml")
	}
	if len(cfg.Go) != 1 {
		return s, nil
	}

	travisRe := regexp.MustCompile(`(?m:^(\s*-\s*)(?:\d\.\d+)(\.x)?$)`)
	return travisRe.ReplaceAllString(s, "${1}"+version+"${2}"), nil
}

func run(version string, path string) []error {
	m := versionRe.FindSubmatch([]byte(version))
	if len(m) == 0 {
		return []error{errors.Errorf("Invalid version %q, expecting x.y", version)}
	}
	errs := make([]error, 0, 3)
	errf := func(f func() error) {
		err := f()
		if err != nil && !os.IsNotExist(err) {
			errs = append(errs, err)
		}
	}

	errf(func() error {
		return processFile(filepath.Join(path, ".promu.yml"), processPromu)
	})
	errf(func() error {
		return processFile(filepath.Join(path, ".circleci/config.yml"), processCircle)
	})
	errf(func() error {
		return processFile(filepath.Join(path, ".travis.yml"), processTravis)
	})

	return errs
}
