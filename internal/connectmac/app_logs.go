package connectmac

import (
	"fmt"
	"strings"
	"time"
)

func (a App) runLogs(args []string) int {
	if len(args) == 0 {
		fmt.Fprintln(a.Err, "usage: cm logs <list|export|clean> [--output <zip>]")
		return 2
	}
	switch args[0] {
	case "list":
		if len(args) != 1 {
			fmt.Fprintln(a.Err, "usage: cm logs list")
			return 2
		}
		files, err := a.LogManager.List()
		if err != nil {
			fmt.Fprintf(a.Err, "logs list failed: %v\n", err)
			return 1
		}
		fmt.Fprint(a.Out, FormatLogFiles(files))
		return 0
	case "clean":
		if len(args) != 1 {
			fmt.Fprintln(a.Err, "usage: cm logs clean")
			return 2
		}
		if err := a.LogManager.Clean(30 * 24 * time.Hour); err != nil {
			fmt.Fprintf(a.Err, "logs clean failed: %v\n", err)
			return 1
		}
		fmt.Fprintln(a.Out, "cleaned logs older than 30 days")
		return 0
	case "export":
		output, err := parseLogsExportArgs(args[1:])
		if err != nil {
			fmt.Fprintln(a.Err, err)
			return 2
		}
		path, err := a.LogManager.Export(output, 30*24*time.Hour)
		if err != nil {
			fmt.Fprintf(a.Err, "logs export failed: %v\n", err)
			return 1
		}
		fmt.Fprintf(a.Out, "exported logs: %s\n", path)
		return 0
	default:
		fmt.Fprintf(a.Err, "unknown logs command %q\n", args[0])
		return 2
	}
}

func parseLogsExportArgs(args []string) (string, error) {
	output := ""
	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--output", "-o":
			i++
			if i >= len(args) || args[i] == "" {
				return "", fmt.Errorf("--output requires a value")
			}
			output = args[i]
		default:
			return "", fmt.Errorf("unknown logs export option %q", args[i])
		}
	}
	return output, nil
}

func FormatLogFiles(files []LogFile) string {
	if len(files) == 0 {
		return "No logs.\n"
	}
	rows := [][]string{{"FILE", "SIZE", "UPDATED"}}
	for _, file := range files {
		rows = append(rows, []string{
			file.Name,
			fmt.Sprintf("%d", file.Size),
			file.ModTime.Format(time.RFC3339),
		})
	}
	return strings.TrimRight(formatRows(rows), "\n") + "\n"
}
