package events

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strconv"
	"time"

	dockerevents "github.com/moby/moby/api/types/events"
	"github.com/moby/moby/client"
)

const (
	backoffInitial = 1 * time.Second
	backoffMax     = 30 * time.Second
)

// EventClient is the subset of the Docker client used by Watcher.
type EventClient interface {
	Events(ctx context.Context, options client.EventsListOptions) client.EventsResult
}

// Notifier delivers an abnormal-event notification.
type Notifier interface {
	Notify(ctx context.Context, n *Notification) error
}

// Watcher subscribes to the Docker event stream, classifies events and
// forwards abnormal ones to the Notifier, reconnecting with backoff on failure.
type Watcher struct {
	client     EventClient
	classifier *Classifier
	notifier   Notifier
	logger     *slog.Logger
}

// NewWatcher wires a Watcher from its collaborators.
func NewWatcher(c EventClient, classifier *Classifier, notifier Notifier, logger *slog.Logger) *Watcher {
	return &Watcher{client: c, classifier: classifier, notifier: notifier, logger: logger}
}

// Run streams events until ctx is cancelled. It only asks the daemon for
// container events; abnormality is decided client-side to avoid server-side
// filter quirks (e.g. health_status). It returns ctx.Err() on shutdown.
func (w *Watcher) Run(ctx context.Context) error {
	filters := client.Filters{}.Add("type", string(dockerevents.ContainerEventType))
	var since string
	backoff := backoffInitial

	for {
		if err := ctx.Err(); err != nil {
			return err
		}

		connectedAt := time.Now()
		res := w.client.Events(ctx, client.EventsListOptions{Filters: filters, Since: since})
		streamErr := w.consume(ctx, res, &since)

		if err := ctx.Err(); err != nil {
			return err
		}

		// A connection that stayed up a while is healthy; reset the backoff so
		// an isolated drop doesn't accumulate long waits.
		if time.Since(connectedAt) >= backoffMax {
			backoff = backoffInitial
		}
		w.logger.Warn("event stream disconnected, reconnecting",
			slog.Duration("backoff", backoff), slog.Any("error", streamErr))

		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(backoff):
		}
		backoff = min(backoff*2, backoffMax)
	}
}

// consume drains one connection's channels until it errors or closes, advancing
// the resume cursor as events arrive.
func (w *Watcher) consume(ctx context.Context, res client.EventsResult, since *string) error {
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case err := <-res.Err:
			if err == nil {
				return errors.New("event error channel closed")
			}
			return err
		case msg, ok := <-res.Messages:
			if !ok {
				return errors.New("event message channel closed")
			}
			*since = resumeCursor(msg)
			w.handle(ctx, msg)
		}
	}
}

func (w *Watcher) handle(ctx context.Context, msg dockerevents.Message) {
	n := w.classifier.Classify(msg)
	if n == nil {
		return
	}
	w.logger.Info("abnormal event",
		slog.String("container", n.Container),
		slog.String("image", n.Image),
		slog.String("action", n.Action),
		slog.String("exitCode", n.ExitCode),
		slog.String("severity", string(n.Severity)),
	)
	if err := w.notifier.Notify(ctx, n); err != nil {
		w.logger.Error("failed to send notification", slog.Any("error", err))
	}
}

// resumeCursor formats a Docker "since" value one nanosecond after the given
// event so a reconnect resumes without replaying the last handled event.
func resumeCursor(msg dockerevents.Message) string {
	if msg.TimeNano > 0 {
		next := msg.TimeNano + 1
		return fmt.Sprintf("%d.%09d", next/int64(time.Second), next%int64(time.Second))
	}
	return strconv.FormatInt(msg.Time, 10)
}
