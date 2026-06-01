package main

import (
	"connectmac/internal/connectmac"
	"context"
	"os"
)

func main() {
	app := connectmac.NewApp(os.Stdout, os.Stderr)
	os.Exit(app.Run(context.Background(), os.Args[1:]))
}
