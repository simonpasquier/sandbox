package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"os"
	"time"

	"github.com/prometheus/client_golang/api"
	"github.com/prometheus/client_golang/api/prometheus/v1"
	"github.com/prometheus/common/model"
)

var (
	help         bool
	url, metric  string
	vrange, step = "1m", "10s"
	start, end   string
)

func init() {
	flag.BoolVar(&help, "help", false, "Help message")
	flag.StringVar(&url, "url", "", "Prometheus address")
	flag.StringVar(&metric, "metric", "", "Prometheus query")
	flag.StringVar(&vrange, "range", "1m", "Vector range (default: 1m)")
	flag.StringVar(&step, "step", "10s", "Step interval (default: 10s)")
	flag.StringVar(&start, "start", "", "Start time for range query (default: now - <range>)")
	flag.StringVar(&end, "end", "", "End time for range query (default: now)")
}

func display(w io.Writer, res model.Value) {
	switch res.Type() {
	case model.ValMatrix:
		matrix := res.(model.Matrix)
		for _, sset := range matrix {
			fmt.Fprintf(w, "metric: %s\n", sset.Metric)
			fmt.Fprintf(w, "samples:\n")
			for _, sample := range sset.Values {
				fmt.Fprintf(w, "\t%s\t%s\n", sample.Timestamp.Time(), sample.Value)
			}
		}
	case model.ValVector:
		vector := res.(model.Vector)
		for _, sample := range vector {
			fmt.Fprintf(w, "metric: %s\n", sample.Metric)
			fmt.Fprintf(w, "samples:\n")
			fmt.Fprintf(w, "\t%s\t%s\n", sample.Timestamp.Time(), sample.Value)
		}
	}
}

func main() {
	var err error
	var tstart, tend time.Time
	var drange model.Duration
	var dstep time.Duration

	flag.Parse()
	if help {
		fmt.Fprintln(os.Stderr, "Prometheus query tool")
		flag.PrintDefaults()
		os.Exit(0)
	}

	if metric == "" {
		fmt.Fprintln(os.Stderr, "Missing --metric parameter.")
		flag.PrintDefaults()
		os.Exit(1)
	}
	if url == "" {
		fmt.Fprintln(os.Stderr, "Missing --url parameter.")
		flag.PrintDefaults()
		os.Exit(1)
	}

	drange, err = model.ParseDuration(vrange)
	if err != nil {
		fmt.Fprintln(os.Stderr, "Invalid range parameter '", vrange, "':", err)
		os.Exit(1)
	}
	dstep, err = time.ParseDuration(step)
	if err != nil {
		fmt.Fprintln(os.Stderr, "Invalid step parameter '", step, "':", err)
		os.Exit(1)
	}

	now := time.Now().Truncate(time.Second)
	if end == "" {
		tend = now
	} else {
		tend, err = time.Parse(time.RFC3339, end)
		if err != nil {
			fmt.Fprintln(os.Stderr, "Invalid end parameter '", end, "':", err)
			os.Exit(1)
		}
	}
	if start == "" {
		tstart = tend.Add(time.Duration(-drange))
	} else {
		tstart, err = time.Parse(time.RFC3339, start)
		if err != nil {
			fmt.Fprintln(os.Stderr, "Invalid start parameter '", start, "':", err)
			os.Exit(1)
		}
	}

	client, err := api.NewClient(api.Config{Address: url})
	if err != nil {
		panic(err)
	}
	api := v1.NewAPI(client)
	ctx := context.Background()

	res, err := api.QueryRange(ctx, metric, v1.Range{Start: tstart, End: tend, Step: dstep})
	if err != nil {
		fmt.Fprintln(os.Stderr, "Error querying Prometheus", metric, ":", err)
		os.Exit(1)
	}
	fmt.Fprintf(os.Stdout, "Getting %s between %s and %s with a step of %s\n", metric, tstart, tend, dstep)
	display(os.Stdout, res)

	q := fmt.Sprintf("%s[%s]", metric, drange)
	res, err = api.Query(ctx, q, tend)
	if err != nil {
		fmt.Fprintln(os.Stderr, "Error querying Prometheus", q, ":", err)
		os.Exit(1)
	}
	fmt.Fprintf(os.Stdout, "Getting %s at instant %s\n", q, tend)
	display(os.Stdout, res)

	q = fmt.Sprintf("rate(%s[%s])", metric, drange)
	res, err = api.Query(ctx, q, tend)
	if err != nil {
		fmt.Fprintln(os.Stderr, "Error querying Prometheus", q, ":", err)
		os.Exit(1)
	}
	fmt.Fprintf(os.Stdout, "Getting %s at instant %s\n", q, tend)
	display(os.Stdout, res)

	res, err = api.QueryRange(ctx, q, v1.Range{Start: tstart, End: tend, Step: dstep})
	if err != nil {
		fmt.Fprintln(os.Stderr, "Error querying Prometheus", metric, ":", err)
		os.Exit(1)
	}
	fmt.Fprintf(os.Stdout, "Getting %s between %s and %s with a step of %s\n", q, tstart, tend, dstep)
	display(os.Stdout, res)
}
