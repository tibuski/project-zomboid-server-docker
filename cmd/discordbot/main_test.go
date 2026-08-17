package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/tibuski/project-zomboid-server-docker/internal/discordbot"
)

func TestEnvDuration(t *testing.T) {
	t.Setenv("POLL_INTERVAL", "10s")
	if got := envDuration("POLL_INTERVAL", 5*time.Second); got != 10*time.Second {
		t.Errorf("envDuration = %s, want 10s", got)
	}
	if got := envDuration("RESTART_COOLDOWN", 5*time.Minute); got != 5*time.Minute {
		t.Errorf("envDuration default = %s, want 5m", got)
	}
	t.Setenv("POLL_INTERVAL", "banana")
	if got := envDuration("POLL_INTERVAL", 5*time.Second); got != 5*time.Second {
		t.Errorf("envDuration invalid = %s, want default 5s", got)
	}
}

// The restart must pull the image and then force-recreate only the game
// service; a full "compose down" would kill the sidecar itself.
func TestRestartServerRunsPullThenRecreate(t *testing.T) {
	var calls [][]string
	orig := runCompose
	runCompose = func(_ context.Context, dir string, args ...string) error {
		if dir != "/compose" {
			t.Errorf("compose dir = %q, want /compose", dir)
		}
		calls = append(calls, append([]string(nil), args...))
		return nil
	}
	defer func() { runCompose = orig }()

	var posted []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Content string `json:"content"`
		}
		_ = json.NewDecoder(r.Body).Decode(&body)
		posted = append(posted, body.Content)
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	client := &discordbot.Client{APIBase: srv.URL, Token: "t", ChannelID: "1"}
	restartServer(context.Background(), client, "/compose", "zomboid", "My Server")

	want := [][]string{
		{"pull", "zomboid"},
		{"up", "-d", "--force-recreate", "zomboid"},
	}
	if !reflect.DeepEqual(calls, want) {
		t.Errorf("compose calls = %v, want %v", calls, want)
	}
	if len(posted) != 2 || !strings.Contains(posted[0], "Restarting") || !strings.Contains(posted[1], "booting") {
		t.Errorf("posted = %v, want restart + success confirmations", posted)
	}
}

// A failed pull must abort the restart before any recreate happens.
func TestRestartServerAbortsWhenPullFails(t *testing.T) {
	var calls [][]string
	orig := runCompose
	runCompose = func(_ context.Context, _ string, args ...string) error {
		calls = append(calls, append([]string(nil), args...))
		return context.DeadlineExceeded
	}
	defer func() { runCompose = orig }()

	var posted []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Content string `json:"content"`
		}
		_ = json.NewDecoder(r.Body).Decode(&body)
		posted = append(posted, body.Content)
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	client := &discordbot.Client{APIBase: srv.URL, Token: "t", ChannelID: "1"}
	restartServer(context.Background(), client, "/compose", "zomboid", "My Server")

	if len(calls) != 1 {
		t.Errorf("compose calls = %v, want only the pull", calls)
	}
	if len(posted) != 2 || !strings.Contains(posted[1], "aborted") {
		t.Errorf("posted = %v, want restart + abort messages", posted)
	}
}
