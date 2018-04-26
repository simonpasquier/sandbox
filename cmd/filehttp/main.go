package main

import (
	"flag"
	"fmt"
	"io/ioutil"
	"log"
	"net/http"
	"os"
)

var (
	help         bool
	listen, file string
)

func init() {
	flag.BoolVar(&help, "help", false, "Help message")
	flag.StringVar(&listen, "listen-address", ":8080", "Listen address")
	flag.StringVar(&file, "file", "", "File to serve")
}

func main() {
	flag.Parse()
	if help {
		fmt.Fprintln(os.Stderr, "Simple HTTP server rendering a static file")
		flag.PrintDefaults()
		os.Exit(0)
	}

	if file == "" {
		fmt.Fprintln(os.Stderr, "Missing --file parameter.")
		flag.PrintDefaults()
		os.Exit(1)
	}

	b, err := ioutil.ReadFile(file)
	if err != nil {
		log.Fatal("Error reading", file, ":", err)
	}

	http.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		_, err := w.Write(b)

		if err != nil {
			log.Println("Failed to write response:", err)
		}
	})

	log.Println("Listening on", listen)
	log.Fatal(http.ListenAndServe(listen, nil))
}
