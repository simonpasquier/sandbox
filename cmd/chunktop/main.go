package main

import (
	"fmt"
	"github.com/prometheus/prometheus/pkg/labels"
	"github.com/prometheus/prometheus/tsdb/chunks"
	"github.com/prometheus/prometheus/tsdb/index"
)

func main() {
	idx, err := index.NewFileReader("data/01EAHCER7GQQA2S9320PHY58DD/index")
	if err != nil {
		fmt.Println("index:", err)
		return
	}
	chunkReader, err := chunks.NewDirReader("data/01EAHCER7GQQA2S9320PHY58DD/chunks", nil)
	if err != nil {
		fmt.Println("chunks:", err)
		return
	}
	defer chunkReader.Close()

	postings, err := idx.Postings("", "")
	if err != nil {
		fmt.Println("postings:", err)
		return
	}
	for postings.Next() {
		if err := postings.Err(); err != nil {
			fmt.Println("postings next:", err)
			return
		}
		id := postings.At()
		fmt.Printf("series id: %d\n", id)

		lbls := make(labels.Labels, 0)
		chks := make([]chunks.Meta, 0)
		err = idx.Series(id, &lbls, &chks)
		if err := postings.Err(); err != nil {
			fmt.Printf("series %d: %s\n", id, err)
			return
		}
		fmt.Printf("series %d, labels: %s, chunks: %d\n", id, lbls.String(), len(chks))

		for i, chkMeta := range chks {
			fmt.Printf("chunk %d, ref: %d", i, chkMeta.Ref)
			chunk, err := chunkReader.Chunk(chkMeta.Ref)
			if err != nil {
				fmt.Printf("chunk: %s\n", err)
				return
			}
			fmt.Printf(", encoding: %s, size: %d, bytes: %d\n", chunk.Encoding(), chunk.NumSamples(), len(chunk.Bytes()))
		}
	}
}
