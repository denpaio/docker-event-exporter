package events

import (
	"testing"
	"time"

	dockerevents "github.com/moby/moby/api/types/events"
)

func containerMsg(action dockerevents.Action, attrs map[string]string) dockerevents.Message {
	return dockerevents.Message{
		Type:     dockerevents.ContainerEventType,
		Action:   action,
		Actor:    dockerevents.Actor{ID: "c-" + attrs["name"], Attributes: attrs},
		TimeNano: time.Now().UnixNano(),
	}
}

func newClassifier() *Classifier {
	return NewClassifier("node-1", map[string]struct{}{"0": {}}, 30*time.Second, 30*time.Second)
}

func TestClassify_OOM(t *testing.T) {
	c := newClassifier()
	n := c.Classify(containerMsg(dockerevents.ActionOOM, map[string]string{"name": "web", "image": "nginx:latest"}))
	if n == nil {
		t.Fatal("expected a notification for oom")
	}
	if n.Severity != SeverityCritical || n.Color != colorCritical {
		t.Errorf("got severity=%q color=%q, want critical/%s", n.Severity, n.Color, colorCritical)
	}
	if n.Container != "web" || n.Image != "nginx:latest" || n.Node != "node-1" {
		t.Errorf("unexpected fields: %+v", n)
	}
}

func TestClassify_DieCleanExitIgnored(t *testing.T) {
	c := newClassifier()
	if n := c.Classify(containerMsg(dockerevents.ActionDie, map[string]string{"name": "web", "exitCode": "0"})); n != nil {
		t.Fatalf("expected nil for die exit 0, got %+v", n)
	}
}

func TestClassify_DieNonZeroExit(t *testing.T) {
	c := newClassifier()
	n := c.Classify(containerMsg(dockerevents.ActionDie, map[string]string{"name": "web", "exitCode": "137"}))
	if n == nil {
		t.Fatal("expected notification for die exit 137")
	}
	if n.Severity != SeverityCritical || n.ExitCode != "137" {
		t.Errorf("got severity=%q exitCode=%q", n.Severity, n.ExitCode)
	}
}

func TestClassify_DieAfterStopSignalSuppressed(t *testing.T) {
	c := newClassifier()
	// docker stop / compose down / Ctrl-C: a kill (stop signal) precedes the die.
	// The container may exit with any non-zero code (e.g. an app that traps
	// SIGTERM and exits 1), so this must be recognised as an intentional stop.
	kill := containerMsg(dockerevents.ActionKill, map[string]string{"name": "web", "signal": "15"})
	if n := c.Classify(kill); n != nil {
		t.Fatalf("kill should not notify, got %+v", n)
	}
	die := containerMsg(dockerevents.ActionDie, map[string]string{"name": "web", "exitCode": "1"})
	if n := c.Classify(die); n != nil {
		t.Fatalf("die after a stop signal should be suppressed, got %+v", n)
	}
}

func TestClassify_DieLongAfterStopSignalNotifies(t *testing.T) {
	c := newClassifier()
	kill := containerMsg(dockerevents.ActionKill, map[string]string{"name": "web", "signal": "15"})
	c.Classify(kill)
	// A death well outside the grace window is unrelated to the earlier stop
	// signal and must still be reported as a crash.
	die := containerMsg(dockerevents.ActionDie, map[string]string{"name": "web", "exitCode": "1"})
	die.TimeNano = kill.TimeNano + int64(2*time.Minute)
	if n := c.Classify(die); n == nil {
		t.Fatal("die long after a stop signal should notify as a crash")
	}
}

func TestClassify_StopSignalMatchedOncePerContainer(t *testing.T) {
	c := newClassifier()
	c.Classify(containerMsg(dockerevents.ActionKill, map[string]string{"name": "web", "signal": "15"}))
	if n := c.Classify(containerMsg(dockerevents.ActionDie, map[string]string{"name": "web", "exitCode": "1"})); n != nil {
		t.Fatalf("first die after stop should be suppressed, got %+v", n)
	}
	// A later, independent crash of the same container (no fresh stop signal)
	// must not be swallowed by a stale kill record.
	if n := c.Classify(containerMsg(dockerevents.ActionDie, map[string]string{"name": "web", "exitCode": "1"})); n == nil {
		t.Fatal("a subsequent crash without a new stop signal should notify")
	}
}

func TestClassify_Unhealthy(t *testing.T) {
	c := newClassifier()
	n := c.Classify(containerMsg(dockerevents.ActionHealthStatusUnhealthy, map[string]string{"name": "web"}))
	if n == nil {
		t.Fatal("expected notification for unhealthy")
	}
	if n.Severity != SeverityWarning || n.Color != colorWarning {
		t.Errorf("got severity=%q color=%q, want warning/%s", n.Severity, n.Color, colorWarning)
	}
}

func TestClassify_Ignored(t *testing.T) {
	c := newClassifier()
	cases := []dockerevents.Message{
		containerMsg("start", map[string]string{"name": "web"}),
		containerMsg("health_status: healthy", map[string]string{"name": "web"}),
		{Type: dockerevents.NetworkEventType, Action: dockerevents.ActionOOM},
	}
	for _, msg := range cases {
		if n := c.Classify(msg); n != nil {
			t.Errorf("action=%q type=%q: expected nil, got %+v", msg.Action, msg.Type, n)
		}
	}
}

func TestClassify_CustomIgnoreExitCodes(t *testing.T) {
	c := NewClassifier("node-1", map[string]struct{}{"0": {}, "143": {}}, 30*time.Second, 30*time.Second)
	if n := c.Classify(containerMsg(dockerevents.ActionDie, map[string]string{"name": "web", "exitCode": "143"})); n != nil {
		t.Fatalf("expected 143 to be ignored, got %+v", n)
	}
	if n := c.Classify(containerMsg(dockerevents.ActionDie, map[string]string{"name": "api", "exitCode": "1"})); n == nil {
		t.Fatal("expected exit 1 to notify")
	}
}

func TestClassify_CooldownDedup(t *testing.T) {
	c := newClassifier()
	now := time.Unix(1_000_000, 0)
	c.now = func() time.Time { return now }

	msg := containerMsg(dockerevents.ActionOOM, map[string]string{"name": "web"})
	if c.Classify(msg) == nil {
		t.Fatal("first oom should notify")
	}
	if c.Classify(msg) != nil {
		t.Fatal("second oom within cooldown should be suppressed")
	}

	now = now.Add(31 * time.Second)
	if c.Classify(msg) == nil {
		t.Fatal("oom after cooldown should notify again")
	}
}
