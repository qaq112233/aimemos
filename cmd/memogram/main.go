package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/usememos/memogram/internal/admin"
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
		Token:     cfg.BotToken,
		ServerURL: cfg.BotProxyAddr,
		ProxyURL:  cfg.TelegramProxyURL,
		Memos:     memos.NewClient(cfg.ServerAddr),
		Store:     data,
		Access:    cfg.Access,
	}
	if notebook := cfg.Notebook; notebook != nil {
		notebook.ProxyURL = cfg.ProxyURL
		settings, err := admin.OpenSettings(cfg.WebDataPath, notebook)
		if err != nil {
			slog.Error("读取网页设置失败", slog.Any("err", err))
			return 1
		}
		if cfg.WebAddr != "" {
			handler, err := admin.NewHandler(settings)
			if err != nil {
				slog.Error("初始化网页配置失败", slog.Any("err", err))
				return 1
			}
			listener, err := net.Listen("tcp", cfg.WebAddr)
			if err != nil {
				slog.Error("启动网页配置失败", slog.Any("err", err))
				return 1
			}
			server := &http.Server{Handler: handler, ReadHeaderTimeout: 5 * time.Second, ReadTimeout: 15 * time.Second, WriteTimeout: 15 * time.Second, IdleTimeout: time.Minute, MaxHeaderBytes: 16 << 10}
			defer func() { _ = server.Close() }()
			go func() {
				if err := server.Serve(listener); err != nil && !errors.Is(err, http.ErrServerClosed) {
					slog.Error("网页服务退出", slog.Any("err", err))
					stop()
				}
			}()
			slog.Info("网页配置已启动，请通过 HTTPS 反向代理访问", slog.String("listen", listener.Addr().String()))
			opts.WebURL = cfg.WebURL
		}
		opts.Notebook = settings
		opts.NewGenerator = func(fetch workflow.Fetch) workflow.Generator { return ai.New(settings, fetch) }
	}
	service, err := bot.New(opts)
	if err != nil {
		slog.Error("failed to start Memogram", slog.Any("err", err))
		return 1
	}
	service.Start(ctx)
	return 0
}
