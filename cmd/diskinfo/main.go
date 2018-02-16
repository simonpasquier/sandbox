package main

import (
	"flag"
	"fmt"
	"syscall"
)

type diskStatus struct {
	Bs    uint64 `json:"bs"`
	All   uint64 `json:"all"`
	Used  uint64 `json:"used"`
	Free  uint64 `json:"free"`
	Avail uint64 `json:"avail"`
}

// disk usage of path/disk
func diskUsage(path string) (*diskStatus, error) {
	disk := &diskStatus{}
	fs := syscall.Statfs_t{}
	err := syscall.Statfs(path, &fs)
	if err != nil {
		return disk, err
	}
	disk.Bs = uint64(fs.Bsize)
	disk.All = fs.Blocks * uint64(fs.Bsize)
	disk.Free = fs.Bfree * uint64(fs.Bsize)
	disk.Avail = fs.Bavail * uint64(fs.Bsize)
	disk.Used = disk.All - disk.Free
	return disk, nil
}

func main() {
	var path string
	flag.StringVar(&path, "path", "/", "Path to inspect")
	flag.Parse()
	fmt.Printf("Disk stats from %s\n", path)
	disk, err := diskUsage(path)
	if err != nil {
		panic(err)
	}
	fmt.Printf("\nAll: %dB\n", disk.All)
	fmt.Printf("Used: %dB\n", disk.Used)
	fmt.Printf("Avail: %dB\n", disk.Avail)
	fmt.Printf("(Block size:: %dB\n)", disk.Bs)
}
