package main

import (
	"fmt"
	"os"
	"time"

	"github.com/oklog/ulid"
)

func main() {
	ul := ulid.MustParseStrict(os.Args[1])
	t := int64(ul.Time())
	fmt.Println("time:", time.Unix(t/1e3, (t%1e3)*1e6).UTC(), "entropy:", fmt.Sprintf("%x", ul.Entropy()))
}
