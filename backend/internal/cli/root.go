package cmds

import (
	"context"
	"log/slog"
	"os"

	"github.com/spf13/cobra"

	"github.com/EHLO1/project-dysfunctional/backend/internal/bootstrap"
	"github.com/EHLO1/project-dysfunctional/backend/pkg/utils/signals"
)

var rootCmd = &cobra.Command{
	Use:          "callout",
	Long:         "Callout - Chat, Voice, Party Up, Don't Wipe the Raid.",
	SilenceUsage: true,
	Run: func(cmd *cobra.Command, args []string) {
		err := bootstrap.Bootstrap(cmd.Context())
		if err != nil {
			slog.Error("Failed to run Callout", "error", err)
			os.Exit(1)
		}
	},
}

func Execute() {
	ctx := signals.SignalContext(context.Background())

	err := rootCmd.ExecuteContext(ctx)
	if err != nil {
		os.Exit(1)
	}
}
