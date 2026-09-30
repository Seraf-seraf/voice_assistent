package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"os/signal"

	"github.com/Seraf-seraf/voice_assistent/internal/bootstrap"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintf(os.Stderr, "Ошибка запуска: %v\n", err)
		os.Exit(1)
	}
}

func run() error {
	configPath := flag.String("config", "", "путь к YAML-конфигурации")
	flag.Parse()

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()
	return bootstrap.Run(ctx, *configPath)
}
