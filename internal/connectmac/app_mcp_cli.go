package connectmac

import (
	"context"
	"fmt"
	"os"
)

func (a App) runMCP(ctx context.Context, configPath string, args []string) int {
	if len(args) > 0 {
		switch args[0] {
		case "--help", "-h", "help":
			a.printMCPUsage()
			return 0
		case "tools":
			return a.runMCPTools(args[1:])
		default:
			fmt.Fprintf(a.Err, "unknown mcp command %q\n\n", args[0])
			a.printMCPUsage()
			return 2
		}
	}
	server := MCPServer{App: a, ConfigPath: configPath}
	if err := server.Serve(ctx, os.Stdin, a.Out); err != nil {
		fmt.Fprintf(a.Err, "mcp failed: %v\n", err)
		return 1
	}
	return 0
}
func (a App) runMCPTools(args []string) int {
	jsonOutput := false
	for _, arg := range args {
		switch arg {
		case "--json":
			jsonOutput = true
		case "--help", "-h":
			a.printMCPUsage()
			return 0
		default:
			fmt.Fprintf(a.Err, "unknown mcp tools option %q\n", arg)
			return 2
		}
	}
	if jsonOutput {
		if err := WriteMCPToolsJSON(a.Out); err != nil {
			fmt.Fprintf(a.Err, "mcp tools failed: %v\n", err)
			return 1
		}
		return 0
	}
	fmt.Fprint(a.Out, FormatMCPToolsText())
	return 0
}
func (a App) printMCPUsage() {
	fmt.Fprint(a.Out, `Usage:
  cm mcp [--config <path>]
  cm mcp tools
  cm mcp tools --json

cm mcp starts the stdio MCP server. It waits for JSON-RPC messages on stdin
and does not print a tool list when run directly.

Use cm mcp tools for a human-readable tool list, or cm mcp tools --json for
the MCP tools/list result JSON.
`)
}
