// Command docker-event-exporter streams Docker daemon events and posts abnormal
// container events (OOM, non-zero exit, unhealthy) to Slack.
package main

import (
	"context"
	"errors"
	"flag"
	"log/slog"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/moby/moby/client"

	"github.com/denpaio/docker-event-exporter/internal/config"
	"github.com/denpaio/docker-event-exporter/internal/events"
	"github.com/denpaio/docker-event-exporter/internal/notifier"
)

func main() {
	testMode := flag.Bool("test", false, "send a test notification to Slack and exit")
	flag.Parse()

	logger := slog.New(slog.NewJSONHandler(os.Stderr, nil))

	cfg, err := config.Load()
	if err != nil {
		logger.Error("configuration error", slog.Any("error", err))
		os.Exit(1)
	}

	slackNotifier := notifier.NewSlack(cfg.SlackToken, cfg.SlackChannel)

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	if *testMode {
		if err := slackNotifier.Notify(ctx, testNotification(cfg.NodeName)); err != nil {
			logger.Error("test notification failed", slog.Any("error", err))
			os.Exit(1)
		}
		logger.Info("test notification sent", slog.String("channel", cfg.SlackChannel))
		return
	}

	dockerClient, err := client.New(client.FromEnv)
	if err != nil {
		logger.Error("failed to create docker client", slog.Any("error", err))
		os.Exit(1)
	}
	defer func() { _ = dockerClient.Close() }()

	classifier := events.NewClassifier(cfg.NodeName, cfg.IgnoreExitCodes, cfg.Cooldown)
	watcher := events.NewWatcher(dockerClient, classifier, slackNotifier, logger)

	logger.Info("starting docker-event-exporter",
		slog.String("node", cfg.NodeName),
		slog.String("channel", cfg.SlackChannel),
		slog.Duration("cooldown", cfg.Cooldown),
	)

	if err := watcher.Run(ctx); err != nil && !errors.Is(err, context.Canceled) {
		logger.Error("watcher stopped", slog.Any("error", err))
		os.Exit(1)
	}
	logger.Info("shutdown complete")
}

func testNotification(node string) *events.Notification {
	return &events.Notification{
		Node:      node,
		Container: "connectivity-test",
		Image:     "docker-event-exporter",
		Action:    "oom",
		ExitCode:  "137",
		Severity:  events.SeverityCritical,
		Color:     "#e01e5a",
		Time:      time.Now(),
	}
}
