package main

import (
	"os"

	"github.com/locke-inc/directory-connector/cmd"
)

func main() {
	if err := cmd.Execute(); err != nil {
		os.Exit(1)
	}
}
