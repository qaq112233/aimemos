package main

import (
	"context"
	"flag"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"syscall"

	"github.com/usememos/memogram/internal/ai"
	"github.com/usememos/memogram/internal/bot"
	"github.com/usememos/memogram/internal/config"
	"github.com/usememos/memogram/internal/memos"
	"github.com/usememos/memogram/internal/store"
	"github.com/usememos/memogram/internal/version"
	"github.com/usememos/memogram/internal/workflow"
)

func main() {
	os.Exit(run())
}

func run() int {
	showVersion := flag.Bool("version", false, "Print the Memogram version and commit")
	checkConfig := flag.String("check-config", "", "Validate notebook YAML without credentials or network requests")
	flag.Parse()
	if err := version.Validate(); err != nil {
		slog.Error("invalid build metadata", slog.Any("err", err))
		return 1
	}
	if *showVersion {
		fmt.Printf("memogram %s (commit %s)\n", version.Version, version.Commit)
		return 0
	}
	if *checkConfig != "" {
		if _, err := config.ReadNotebook(*checkConfig); err != nil {
			slog.Error("invalid notebook configuration", slog.Any("err", err))
			return 1
		}
		fmt.Println("Notebook configuration is valid")
		return 0
	}
	slog.Info("Memogram", slog.String("version", version.Version), slog.String("commit", version.Commit))
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	cfg, err := config.Load()
	if err != nil {
		slog.Error("invalid configuration", slog.Any("err", err))
		return 1
	}
	data, err := store.Open(cfg.DataPath)
	if err != nil {
		slog.Error("failed to open data file", slog.Any("err", err))
		return 1
	}
	opts := bot.Options{
		Token:            cfg.BotToken,
		ServerURL:        cfg.BotProxyAddr,
		ProxyURL:         cfg.TelegramProxyURL,
		Memos:            memos.NewClient(cfg.ServerAddr),
		Store:            data,
		AllowedUsernames: cfg.AllowedUsernames,
	}
	if path := os.Getenv("NOTEBOOK_CONFIG"); path != "" {
		notebook, err := config.LoadNotebook(path)
		if err != nil {
			slog.Error("invalid notebook configuration", slog.Any("err", err))
			return 1
		}
		opts.Notebook = notebook
		notebook.ProxyURL = cfg.ProxyURL
		opts.NewGenerator = func(fetch workflow.Fetch) workflow.Generator { return ai.New(notebook, fetch) }
	}
	service, err := bot.New(opts)
	if err != nil {
		slog.Error("failed to start Memogram", slog.Any("err", err))
		return 1
	}
	service.Start(ctx)
	return 0
}
