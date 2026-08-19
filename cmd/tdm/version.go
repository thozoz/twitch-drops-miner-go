package main

import (
	"fmt"

	"github.com/spf13/cobra"
	"github.com/thozoz/twitch-drops-miner-go/pkg/version"
)

var versionCmd = &cobra.Command{
	Use:   "version",
	Short: "Print the tdm version information",
	Run: func(cmd *cobra.Command, args []string) {
		fmt.Println(version.String())
	},
}

func init() {
	rootCmd.AddCommand(versionCmd)
}
