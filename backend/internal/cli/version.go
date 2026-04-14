package cmds

import (
	"fmt"

	"github.com/spf13/cobra"

	"github.com/EHLO1/project-dysfunctional/backend/internal/common"
)

func init() {
	rootCmd.AddCommand(&cobra.Command{
		Use:   "version",
		Short: "Print the version number",
		Run: func(cmd *cobra.Command, args []string) {
			fmt.Println("callout " + common.Version)
		},
	})
}
