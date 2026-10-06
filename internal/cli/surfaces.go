package cli

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"

	"github.com/j-a-man/visual-branches/internal/mcpserver"
	"github.com/j-a-man/visual-branches/internal/web"
)

func (a *app) mcpCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "mcp",
		Short: "Run the MCP server over stdio for coding agents",
		Long: `Serve vb's tools over the Model Context Protocol on stdin/stdout.

Tools (all read-only): branch_map, branch_detail, changes_since,
check_conflicts, who_touches, cleanup_candidates.

  Claude Code:  claude mcp add vb -- vb mcp
  Codex CLI:    add [mcp_servers.vb] command = "vb", args = ["mcp"] to ~/.codex/config.toml
  Cursor:       add {"mcpServers": {"vb": {"command": "vb", "args": ["mcp"]}}} to .cursor/mcp.json`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			return mcpserver.Run(cmd.Context(), mcpserver.Options{
				Dir:     a.g.dir,
				Version: Version(),
				Offline: a.g.offline || os.Getenv("VB_OFFLINE") == "1",
			})
		},
	}
}

func (a *app) webCmd() *cobra.Command {
	var port int
	var noOpen bool
	cmd := &cobra.Command{
		Use:   "web",
		Short: "Open the branch map in your browser (local, live-updating)",
		Long: `Serve an interactive branch map on 127.0.0.1 and open it in your browser.
The page updates live as branches, worktrees, and pull requests change.
Nothing leaves your machine: the server only listens on the loopback
interface and rejects requests for other host names.

Keys: / filter, j k move, enter open PR, f fit, 1 2 3 switch tabs.`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := cmd.Context()
			s, err := a.session(ctx, false)
			if err != nil {
				return err
			}
			if port == 0 {
				port = s.Config.Web.Port
			}
			open := s.Config.Web.OpenBrowser && !noOpen
			return web.Serve(ctx, web.Options{
				Session: s,
				Port:    port,
				Ready: func(url string) {
					fmt.Fprintf(a.err, "vb web: serving %s (ctrl+c to stop)\n", url)
					if open {
						if err := openBrowser(url); err != nil {
							fmt.Fprintf(a.err, "vb web: could not open a browser: %v\n", err)
						}
					}
				},
			})
		},
	}
	cmd.Flags().IntVarP(&port, "port", "p", 0, "port to listen on (default web.port, 7878)")
	cmd.Flags().BoolVar(&noOpen, "no-open", false, "do not open a browser")
	return cmd
}
