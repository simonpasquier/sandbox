package main

import (
	"flag"
	"fmt"
	"io"
	"os"

	"github.com/prometheus/prometheus/pkg/labels"
	"github.com/prometheus/prometheus/tsdb/record"
	"github.com/prometheus/prometheus/tsdb/wal"
)

var (
	path     string
	matchers string
	help     bool
)

func init() {
	flag.BoolVar(&help, "help", false, "Show help")
	flag.StringVar(&matchers, "matchers", "{__name__=~\".+\"", "Label matchers")
}

func main() {
	flag.Parse()

	if help {
		fmt.Fprintln(os.Stderr, "Analyzes a WAL directory or file")
		flag.PrintDefaults()
		return
	}

	args := flag.Args()
	if len(args) != 1 {
		fmt.Fprintln(os.Stderr, "expecting one argument")
		os.Exit(1)
	}

	path := args[0]

	fi, err := os.Stat(path)
	if err != nil {
		panic(err)
	}

	var rc io.ReadCloser
	if fi.IsDir() {
		rc, err = wal.NewSegmentsReader(path)
	} else {
		rc, err = wal.OpenReadSegment(path)
	}

	if err != nil {
		panic(err)
	}

	r := wal.NewReader(rc)

	var (
		dec     record.Decoder
		series  []record.RefSeries
		samples []record.RefSample
		lbls    = make(map[uint64]labels.Labels)
	)

	for r.Next() {
		rec := r.Record()
		switch dec.Type(rec) {
		case record.Series:
			series, err = dec.Series(rec, series)
			if err != nil {
				fmt.Printf("error while decoding series: %v\n", err)
				break
			}
			for _, s := range series {
				lbls[s.Ref] = s.Labels
				//fmt.Printf("series 0x%X (%d): %v\n", s.Ref, s.Ref, s.Labels.String())
			}
			series = series[:0]
		case record.Samples:
			samples, err = dec.Samples(rec, samples)
			if err != nil {
				fmt.Printf("error while decoding samples: %v\n", err)
				break
			}
			for _, s := range samples {
				lbl, found := lbls[s.Ref]
				if !found {
					continue
				}
				fmt.Printf("%s (ref: 0x%X): %f@%d\n", lbl.String(), s.Ref, s.V, s.T)
			}
			samples = samples[:0]

		}
	}

	if r.Err() != nil {
		fmt.Printf("error while reading WAL: %v\n", r.Err())
	}
}
