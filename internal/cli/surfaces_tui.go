package cli

import (
	"context"
	"fmt"

	"github.com/spf13/cobra"

	"github.com/j-a-man/visual-branches/internal/tui"
)

func (a *app) tuiCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "tui",
		Short: "Interactive branch map with details and actions",
		Long: `Browse the branch map interactively. The right pane shows the selected
branch in detail. Keys: s switch, o open PR, y copy name, d delete a merged
branch (asks first), / filter, m toggle merged, a toggle remote-only,
r refresh, ? help, q quit.`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			return a.runTUI(cmd.Context())
		},
	}
}

func (a *app) runTUI(ctx context.Context) error {
	s, err := a.session(ctx, false)
	if err != nil {
		return err
	}
	if !a.terminal().tty {
		return fmt.Errorf("vb tui needs an interactive terminal; use vb or vb --agent instead")
	}
	return tui.Run(ctx, tui.Options{Session: s, Open: openBrowser})
}
