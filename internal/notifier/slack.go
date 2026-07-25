// Package notifier delivers abnormal-event notifications to Slack.
package notifier

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"

	"github.com/slack-go/slack"

	"github.com/denpaio/docker-event-exporter/internal/events"
)

// Slack posts notifications to a channel via chat.postMessage using a bot token.
type Slack struct {
	client  *slack.Client
	channel string
}

// NewSlack builds a Slack notifier for the given bot token and channel.
func NewSlack(token, channel string) *Slack {
	return &Slack{client: slack.New(token), channel: channel}
}

// Notify posts a colour-coded attachment describing the abnormal event.
func (s *Slack) Notify(ctx context.Context, n *events.Notification) error {
	_, _, err := s.client.PostMessageContext(
		ctx,
		s.channel,
		slack.MsgOptionText(fallbackText(n), false),
		slack.MsgOptionAttachments(buildAttachment(n)),
	)
	if err != nil {
		return fmt.Errorf("post slack message: %w", err)
	}
	return nil
}

func buildAttachment(n *events.Notification) slack.Attachment {
	fields := []slack.AttachmentField{
		{Title: "Node", Value: n.Node, Short: true},
		{Title: "Container", Value: orDash(n.Container), Short: true},
		{Title: "Image", Value: orDash(n.Image), Short: false},
	}
	if n.ExitCode != "" {
		fields = append(fields, slack.AttachmentField{Title: "Exit Code", Value: n.ExitCode, Short: true})
	}
	if n.Signal != "" {
		fields = append(fields, slack.AttachmentField{Title: "Signal", Value: n.Signal, Short: true})
	}

	return slack.Attachment{
		Color:  n.Color,
		Title:  title(n),
		Fields: fields,
		Footer: "docker-event-exporter",
		Ts:     json.Number(strconv.FormatInt(n.Time.Unix(), 10)),
	}
}

func title(n *events.Notification) string {
	emoji := "🔴"
	if n.Severity == events.SeverityWarning {
		emoji = "🟡"
	}
	return fmt.Sprintf("%s %s: %s", emoji, actionLabel(n.Action), orDash(n.Container))
}

func fallbackText(n *events.Notification) string {
	return fmt.Sprintf("[%s] %s on %s (%s)", n.Severity, actionLabel(n.Action), n.Node, orDash(n.Container))
}

func actionLabel(action string) string {
	switch {
	case action == "oom":
		return "OOM"
	case action == "die":
		return "Container died"
	case strings.HasPrefix(action, "health_status"):
		return "Health check unhealthy"
	default:
		return action
	}
}

func orDash(s string) string {
	if s == "" {
		return "-"
	}
	return s
}
