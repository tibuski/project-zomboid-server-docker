package discordbot

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestIsRestartCommand(t *testing.T) {
	for _, tc := range []struct {
		content string
		want    bool
	}{
		{"restart server", true},
		{"Restart Server", true},
		{"RESTART SERVER", true},
		{"  restart server  ", true},
		{"restart server!", false},
		{"please restart server", false},
		{"restart", false},
		{"", false},
	} {
		if got := IsRestartCommand(tc.content); got != tc.want {
			t.Errorf("IsRestartCommand(%q) = %v, want %v", tc.content, got, tc.want)
		}
	}
}

// fakeDiscord serves the channel messages endpoint and records requests.
type fakeDiscord struct {
	t        *testing.T
	messages []Message
	gotAfter string
	gotAuth  string
	posted   []string
}

func (f *fakeDiscord) server() *httptest.Server {
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		f.gotAuth = r.Header.Get("Authorization")
		switch {
		case r.Method == http.MethodGet && strings.HasSuffix(r.URL.Path, "/messages"):
			f.gotAfter = r.URL.Query().Get("after")
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(f.messages)
		case r.Method == http.MethodPost && strings.HasSuffix(r.URL.Path, "/messages"):
			var body struct {
				Content string `json:"content"`
			}
			_ = json.NewDecoder(r.Body).Decode(&body)
			f.posted = append(f.posted, body.Content)
			w.WriteHeader(http.StatusOK)
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
}

func testClient(srv *httptest.Server) *Client {
	return &Client{APIBase: srv.URL, Token: "bot-token", ChannelID: "123"}
}

func msg(id, content string, bot bool) Message {
	var m Message
	m.ID = id
	m.Content = content
	m.Author.Bot = bot
	return m
}

func TestMessagesAfter(t *testing.T) {
	f := &fakeDiscord{messages: []Message{msg("10", "hello", false)}}
	srv := f.server()
	defer srv.Close()

	msgs, err := testClient(srv).MessagesAfter(context.Background(), "9")
	if err != nil {
		t.Fatalf("MessagesAfter: %v", err)
	}
	if f.gotAfter != "9" {
		t.Errorf("after param = %q, want 9", f.gotAfter)
	}
	if f.gotAuth != "Bot bot-token" {
		t.Errorf("Authorization = %q, want bot header", f.gotAuth)
	}
	if len(msgs) != 1 || msgs[0].ID != "10" || msgs[0].Content != "hello" {
		t.Errorf("messages = %+v", msgs)
	}
}

func TestMessagesAfterError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusTooManyRequests)
	}))
	defer srv.Close()

	if _, err := testClient(srv).MessagesAfter(context.Background(), ""); err == nil {
		t.Fatal("expected error on 429")
	}
}

func TestPost(t *testing.T) {
	f := &fakeDiscord{}
	srv := f.server()
	defer srv.Close()

	if err := testClient(srv).Post(context.Background(), "restarting"); err != nil {
		t.Fatalf("Post: %v", err)
	}
	if len(f.posted) != 1 || f.posted[0] != "restarting" {
		t.Errorf("posted = %v, want [restarting]", f.posted)
	}
}

func TestWatcherSkipsHistoryThenTriggers(t *testing.T) {
	f := &fakeDiscord{messages: []Message{msg("10", "restart server", false)}}
	srv := f.server()
	defer srv.Close()
	w := &Watcher{Client: testClient(srv)}

	// First poll establishes the baseline: the pre-existing command must not
	// trigger a restart.
	if ok, err := w.Poll(context.Background()); err != nil || ok {
		t.Fatalf("baseline poll = %v, %v; want false, nil", ok, err)
	}

	f.messages = []Message{msg("11", "RESTART SERVER", false)}
	if ok, err := w.Poll(context.Background()); err != nil || !ok {
		t.Fatalf("command poll = %v, %v; want true, nil", ok, err)
	}
}

func TestWatcherIgnoresBotsAndOtherMessages(t *testing.T) {
	f := &fakeDiscord{}
	srv := f.server()
	defer srv.Close()
	w := &Watcher{Client: testClient(srv)}

	// Arm the baseline on an empty channel.
	if ok, err := w.Poll(context.Background()); err != nil || ok {
		t.Fatalf("arm poll = %v, %v; want false, nil", ok, err)
	}

	f.messages = []Message{
		msg("11", "restart server", true), // bot author: ignored
		msg("12", "can someone restart server?", false),
		msg("13", "hello", false),
	}
	if ok, err := w.Poll(context.Background()); err != nil || ok {
		t.Fatalf("poll = %v, %v; want false, nil", ok, err)
	}
}

func TestWatcherCooldown(t *testing.T) {
	f := &fakeDiscord{}
	srv := f.server()
	defer srv.Close()
	w := &Watcher{Client: testClient(srv), Cooldown: time.Hour}

	if ok, _ := w.Poll(context.Background()); ok {
		t.Fatal("baseline should not trigger")
	}

	f.messages = []Message{msg("11", "restart server", false)}
	if ok, err := w.Poll(context.Background()); err != nil || !ok {
		t.Fatalf("first command = %v, %v; want true, nil", ok, err)
	}

	f.messages = []Message{msg("12", "restart server", false)}
	if ok, err := w.Poll(context.Background()); err != nil || ok {
		t.Fatalf("command within cooldown = %v, %v; want false, nil", ok, err)
	}
}

func TestWatcherAdvancesLastID(t *testing.T) {
	f := &fakeDiscord{}
	srv := f.server()
	defer srv.Close()
	w := &Watcher{Client: testClient(srv)}

	_, _ = w.Poll(context.Background()) // arm on empty channel
	f.messages = []Message{msg("11", "hello", false), msg("12", "world", false)}
	if ok, err := w.Poll(context.Background()); err != nil || ok {
		t.Fatalf("poll = %v, %v; want false, nil", ok, err)
	}
	if f.gotAfter != "" {
		t.Errorf("first fetch after = %q, want empty", f.gotAfter)
	}

	_, _ = w.Poll(context.Background())
	if f.gotAfter != "12" {
		t.Errorf("second fetch after = %q, want 12", f.gotAfter)
	}
}

func ExampleIsRestartCommand() {
	fmt.Println(IsRestartCommand("Restart Server"))
	// Output: true
}
