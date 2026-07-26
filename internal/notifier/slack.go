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

// Notify posts a colour-coded attachment whose title is the event reason and
// whose body describes what happened in plain language.
func (s *Slack) Notify(ctx context.Context, n *events.Notification) error {
	_, _, err := s.client.PostMessageContext(
		ctx,
		s.channel,
		slack.MsgOptionAttachments(buildAttachment(n)),
	)
	if err != nil {
		return fmt.Errorf("post slack message: %w", err)
	}
	return nil
}

func buildAttachment(n *events.Notification) slack.Attachment {
	sentence := describe(n)
	return slack.Attachment{
		Color:    n.Color,
		Title:    reason(n),
		Text:     sentence,
		Fallback: sentence,
		Footer:   n.Node,
		Ts:       json.Number(strconv.FormatInt(n.Time.Unix(), 10)),
	}
}

// reason is the short event name shown as the (bold) attachment title.
func reason(n *events.Notification) string {
	switch {
	case n.Action == "oom":
		return "OOMKilled"
	case n.Action == "die":
		return "ContainerDied"
	case strings.HasPrefix(n.Action, "health_status"):
		return "Unhealthy"
	default:
		return n.Action
	}
}

// describe renders the event as a plain-language sentence.
func describe(n *events.Notification) string {
	subject := containerRef(n)
	switch {
	case n.Action == "oom":
		return fmt.Sprintf("Container %s ran out of memory and was killed.", subject)
	case n.Action == "die":
		return fmt.Sprintf("Container %s exited unexpectedly with code %s.", subject, orDash(n.ExitCode))
	case strings.HasPrefix(n.Action, "health_status"):
		return fmt.Sprintf("Container %s failed its health check and is now unhealthy.", subject)
	default:
		return fmt.Sprintf("Container %s reported event %q.", subject, n.Action)
	}
}

// containerRef names the container, appending its image when known.
func containerRef(n *events.Notification) string {
	name := orDash(n.Container)
	if n.Image != "" {
		return fmt.Sprintf("%s (%s)", name, n.Image)
	}
	return name
}

func orDash(s string) string {
	if s == "" {
		return "-"
	}
	return s
}
