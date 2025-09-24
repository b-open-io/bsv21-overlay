package main

import (
	"os"

	"github.com/b-open-io/bsv21-overlay/cmd"
)

func main() {
	if err := cmd.Execute(); err != nil {
		os.Exit(1)
	}
}