package main

import (
	"connectmac/internal/connectmac"
	"context"
	"os"
)

var version = "dev"

func main() {
	app := connectmac.NewApp(os.Stdout, os.Stderr)
	app.Version = version
	os.Exit(app.Run(context.Background(), os.Args[1:]))
}
