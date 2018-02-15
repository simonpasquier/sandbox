package main

// Since prometheus vendors everything, you need to delete the following
// directories in prometheus/prometheus to compile this program.
// - vendor/golang.org/x/
// - vendor/google.golang.org/grpc/
//
// See https://github.com/prometheus/prometheus/issues/1720
import (
	"context"
	"fmt"
	prompb "github.com/prometheus/prometheus/prompb"
	"google.golang.org/grpc"
)

// A sample client that uses the gRPC interface of Prometheus.
func main() {
	fmt.Println("Starting client...")
	cc, err := grpc.Dial("localhost:9090", grpc.WithInsecure())
	if err != nil {
		panic(err)
	}
	defer cc.Close()
	c := prompb.NewAdminClient(cc)
	ctx := context.Background()
	req := prompb.TSDBCleanTombstonesRequest{}
	res, err := c.TSDBCleanTombstones(ctx, &req)
	if err != nil {
		panic(err)
	}
	fmt.Println("response:", res.String())
}
