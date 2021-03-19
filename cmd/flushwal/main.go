package main

import (
	"io/ioutil"
	"math"
	"os"
	"path/filepath"

	"github.com/go-kit/kit/log"
	"github.com/go-kit/kit/log/level"
	"github.com/pkg/errors"
	"github.com/prometheus/prometheus/tsdb"
	"github.com/prometheus/prometheus/tsdb/wal"
)

func main() {
	logger := log.NewLogfmtLogger(log.NewSyncWriter(os.Stderr))
	logger = level.NewFilter(logger, level.AllowAll())
	if err := run(logger); err != nil {
		level.Error(logger).Log("err", err)
		os.Exit(1)
	}
}

func run(logger log.Logger) error {
	db, err := tsdb.OpenDBReadOnly("data", logger)
	if err != nil {
		return errors.Wrap(err, "opening db")
	}

	dir, err := ioutil.TempDir("data", "tmp")
	if err != nil {
		return errors.Wrap(err, "creating temporary dir")
	}

	defer func() {
		os.RemoveAll(dir)
	}()

	if err := db.FlushWAL(dir); err != nil {
		return errors.Wrap(err, "flushing WAL")
	}

	if err := db.Close(); err != nil {
		return errors.Wrap(err, "closing db")
	}

	blocks, err := ioutil.ReadDir(dir)
	if err != nil {
		return errors.Wrap(err, "reading dir")
	}

	wlog, err := wal.New(logger, nil, "data/wal", false) // do we care about compression at all
	if err != nil {
		return errors.Wrap(err, "opening WAL")
	}

	if err := wlog.Truncate(math.MaxInt64); err != nil {
		return errors.Wrap(err, "truncating WAL")
	}

	if err := wal.DeleteCheckpoints(wlog.Dir(), math.MaxInt32); err != nil {
		return errors.Wrap(err, "deleting checkpoints")
	}

	for _, block := range blocks {
		if !block.IsDir() {
			continue
		}
		if err := os.Rename(filepath.Join(dir, block.Name()), filepath.Join("data", block.Name())); err != nil {
			return errors.Wrap(err, "deleting dir")
		}
	}

	return nil
}
