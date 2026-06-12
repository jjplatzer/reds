package main

import (
	"fmt"
	"os"
)

func main() {
	if len(os.Args) < 2 {
		printUsageAndExit()
	}

	var err error
	switch os.Args[1] {
	case "asdex":
		err = runAsdex(os.Args[2:])
	case "eram":
		err = runEram(os.Args[2:])
	default:
		printUsageAndExit()
	}

	if err != nil {
		fmt.Fprintln(os.Stderr, "crc2reds:", err)
		os.Exit(1)
	}
}

func printUsageAndExit() {
	fmt.Fprintln(os.Stderr, `usage:
  go run ./cmd/crc2reds asdex font    -in /path/to/font.bin.zst -out asdex/assets/font.go
  go run ./cmd/crc2reds asdex cursors -in /path/to/cursors-or-cursor.cur.zst -out asdex/assets/cursors.go
  go run ./cmd/crc2reds eram  font    -in /path/to/eram-fonts-or-font.bin.zst -out eram/assets/font.go`)
	os.Exit(2)
}
