// Package events classifies Docker daemon events and watches the event stream,
// forwarding only abnormal container events to a Notifier.
package events

import (
	"sync"
	"time"

	dockerevents "github.com/moby/moby/api/types/events"
)

// Severity ranks how urgent an abnormal event is.
type Severity string

const (
	SeverityCritical Severity = "critical"
	SeverityWarning  Severity = "warning"
)

// Slack attachment colours per severity.
const (
	colorCritical = "#e01e5a"
	colorWarning  = "#daa038"
)

// Notification is an abnormal container event ready to be delivered.
type Notification struct {
	Node      string
	Container string
	Image     string
	Action    string
	ExitCode  string
	Signal    string
	Severity  Severity
	Color     string
	Time      time.Time
}

// Classifier decides whether a Docker event is abnormal and worth notifying,
// de-duplicating repeated events for the same container+action within a
// cooldown window (Docker can emit e.g. OOM events in bursts).
type Classifier struct {
	node            string
	ignoreExitCodes map[string]struct{}
	cooldown        time.Duration

	mu       sync.Mutex
	lastSeen map[string]time.Time
	now      func() time.Time // overridable clock for tests
}

// NewClassifier constructs a Classifier. node labels the host in notifications,
// ignoreExitCodes are container "die" exit codes to skip, and cooldown is the
// de-duplication window (<= 0 disables de-duplication).
func NewClassifier(node string, ignoreExitCodes map[string]struct{}, cooldown time.Duration) *Classifier {
	return &Classifier{
		node:            node,
		ignoreExitCodes: ignoreExitCodes,
		cooldown:        cooldown,
		lastSeen:        make(map[string]time.Time),
		now:             time.Now,
	}
}

// Classify returns a Notification when msg is an abnormal container event that
// is not currently suppressed by the cooldown, otherwise nil.
func (c *Classifier) Classify(msg dockerevents.Message) *Notification {
	if msg.Type != dockerevents.ContainerEventType {
		return nil
	}

	n := c.build(msg)
	if n == nil {
		return nil
	}
	if c.suppressed(msg.Actor.ID, string(msg.Action)) {
		return nil
	}
	return n
}

func (c *Classifier) build(msg dockerevents.Message) *Notification {
	attr := msg.Actor.Attributes
	notify := func(sev Severity, color string) *Notification {
		return &Notification{
			Node:      c.node,
			Container: attr["name"],
			Image:     attr["image"],
			Action:    string(msg.Action),
			ExitCode:  attr["exitCode"],
			Signal:    attr["signal"],
			Severity:  sev,
			Color:     color,
			Time:      eventTime(msg),
		}
	}

	switch msg.Action {
	case dockerevents.ActionOOM:
		return notify(SeverityCritical, colorCritical)
	case dockerevents.ActionDie:
		if _, ignored := c.ignoreExitCodes[attr["exitCode"]]; ignored {
			return nil
		}
		return notify(SeverityCritical, colorCritical)
	case dockerevents.ActionHealthStatusUnhealthy:
		return notify(SeverityWarning, colorWarning)
	default:
		return nil
	}
}

// suppressed reports whether an identical container+action event was seen
// within the cooldown window; when it returns false it records the event.
func (c *Classifier) suppressed(containerID, action string) bool {
	if c.cooldown <= 0 {
		return false
	}
	key := containerID + "|" + action
	now := c.now()

	c.mu.Lock()
	defer c.mu.Unlock()

	if last, ok := c.lastSeen[key]; ok && now.Sub(last) < c.cooldown {
		return true
	}
	c.lastSeen[key] = now
	c.reap(now)
	return false
}

// reap drops entries whose cooldown has elapsed to bound memory usage.
func (c *Classifier) reap(now time.Time) {
	for k, t := range c.lastSeen {
		if now.Sub(t) >= c.cooldown {
			delete(c.lastSeen, k)
		}
	}
}

func eventTime(msg dockerevents.Message) time.Time {
	switch {
	case msg.TimeNano > 0:
		return time.Unix(0, msg.TimeNano)
	case msg.Time > 0:
		return time.Unix(msg.Time, 0)
	default:
		return time.Now()
	}
}
