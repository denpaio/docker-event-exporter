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
	stopGrace       time.Duration

	mu          sync.Mutex
	lastSeen    map[string]time.Time
	recentKills map[string]time.Time // container ID -> time it last received a stop signal
	now         func() time.Time     // overridable clock for tests
}

// NewClassifier constructs a Classifier. node labels the host in notifications,
// ignoreExitCodes are container "die" exit codes to skip, cooldown is the
// de-duplication window (<= 0 disables de-duplication), and stopGrace is how
// long after a stop signal a container's death still counts as an intentional
// stop rather than a crash (<= 0 disables stop detection).
func NewClassifier(node string, ignoreExitCodes map[string]struct{}, cooldown, stopGrace time.Duration) *Classifier {
	return &Classifier{
		node:            node,
		ignoreExitCodes: ignoreExitCodes,
		cooldown:        cooldown,
		stopGrace:       stopGrace,
		lastSeen:        make(map[string]time.Time),
		recentKills:     make(map[string]time.Time),
		now:             time.Now,
	}
}

// Classify returns a Notification when msg is an abnormal container event that
// is not currently suppressed, otherwise nil. A container death that follows a
// stop signal within stopGrace is treated as an intentional stop and suppressed,
// so Ctrl-C / "docker compose down" don't page as crashes.
func (c *Classifier) Classify(msg dockerevents.Message) *Notification {
	if msg.Type != dockerevents.ContainerEventType {
		return nil
	}

	// A "kill" event is the daemon delivering a stop signal to the container
	// (docker stop / compose down / restart, or an explicit docker kill). Record
	// it so the "die" that follows is recognised as an intentional stop.
	if msg.Action == dockerevents.ActionKill {
		c.recordKill(msg.Actor.ID, eventTime(msg))
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
		if c.stoppedRecently(msg.Actor.ID, eventTime(msg)) {
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

// recordKill notes that a container received a stop signal at t, dropping stale
// records so the map stays bounded.
func (c *Classifier) recordKill(containerID string, t time.Time) {
	if c.stopGrace <= 0 {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	c.recentKills[containerID] = t
	for id, kt := range c.recentKills {
		if t.Sub(kt) >= c.stopGrace {
			delete(c.recentKills, id)
		}
	}
}

// stoppedRecently reports whether the container received a stop signal within
// stopGrace before dying, i.e. the death is an intentional stop rather than a
// crash. It consumes the record so each stop is matched at most once. Events
// arrive in daemon order, so a recorded kill always precedes the die.
func (c *Classifier) stoppedRecently(containerID string, dieTime time.Time) bool {
	if c.stopGrace <= 0 {
		return false
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	kt, ok := c.recentKills[containerID]
	if !ok {
		return false
	}
	delete(c.recentKills, containerID)
	return dieTime.Sub(kt) < c.stopGrace
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
