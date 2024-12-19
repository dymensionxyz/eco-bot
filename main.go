package main

import (
	"log"

	"github.com/dymensionxyz/eco-bot/cmd"
)

func main() {
	if err := cmd.RootCmd.Execute(); err != nil {
		log.Fatalf("failed to execute root command: %v", err)
	}
}
