package commands

import (
	"github.com/spf13/cobra"
)

func All() []*cobra.Command {
	return []*cobra.Command{
		newAuthCmd(),
		newStatsCmd(),
	}
}
