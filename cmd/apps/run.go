package apps

import (
	"github.com/spf13/cobra"
)

func init() {
	RootCmd.AddCommand(runCmd)
}

var runCmd = &cobra.Command{
	Use:   "run",
	Short: "run",
	Long:  `run village net daemon`,
	RunE: func(cmd *cobra.Command, args []string) error {
		return nil
	},
}
