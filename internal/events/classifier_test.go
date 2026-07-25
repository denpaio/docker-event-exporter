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
	return NewClassifier("node-1", map[string]struct{}{"0": {}}, 30*time.Second)
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
	c := NewClassifier("node-1", map[string]struct{}{"0": {}, "143": {}}, 30*time.Second)
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
