package notifier

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/slack-go/slack"

	"github.com/denpaio/docker-event-exporter/internal/events"
)

func newTestSlack(t *testing.T, handler http.HandlerFunc) *Slack {
	t.Helper()
	srv := httptest.NewServer(handler)
	t.Cleanup(srv.Close)
	return &Slack{
		client:  slack.New("xoxb-test", slack.OptionAPIURL(srv.URL+"/")),
		channel: "C123",
	}
}

func TestSlackNotify(t *testing.T) {
	var gotChannel, gotText, gotAttachments string
	s := newTestSlack(t, func(w http.ResponseWriter, r *http.Request) {
		_ = r.ParseForm()
		gotChannel = r.FormValue("channel")
		gotText = r.FormValue("text")
		gotAttachments = r.FormValue("attachments")
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"ok":true,"channel":"C123","ts":"1234.5678"}`))
	})

	n := &events.Notification{
		Node:      "node-1",
		Container: "web",
		Image:     "nginx:latest",
		Action:    "oom",
		ExitCode:  "137",
		Severity:  events.SeverityCritical,
		Color:     "#e01e5a",
		Time:      time.Unix(1_700_000_000, 0),
	}
	if err := s.Notify(context.Background(), n); err != nil {
		t.Fatalf("Notify returned error: %v", err)
	}

	if gotChannel != "C123" {
		t.Errorf("channel = %q, want C123", gotChannel)
	}
	if gotText == "" {
		t.Error("expected non-empty fallback text")
	}
	for _, want := range []string{"#e01e5a", "web", "nginx:latest", "137", "node-1"} {
		if !strings.Contains(gotAttachments, want) {
			t.Errorf("attachments payload missing %q: %s", want, gotAttachments)
		}
	}
}

func TestSlackNotify_APIError(t *testing.T) {
	s := newTestSlack(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"ok":false,"error":"channel_not_found"}`))
	})

	err := s.Notify(context.Background(), &events.Notification{
		Node: "node-1", Action: "oom", Severity: events.SeverityCritical, Color: "#e01e5a", Time: time.Now(),
	})
	if err == nil {
		t.Fatal("expected error when Slack returns ok:false")
	}
	if !strings.Contains(err.Error(), "channel_not_found") {
		t.Errorf("error = %v, want it to contain channel_not_found", err)
	}
}
