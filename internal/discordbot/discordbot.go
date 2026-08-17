// Package discordbot implements a minimal Discord channel watcher: it polls
// a channel for new messages and reports when somebody requests a server
// restart. Discord webhooks can only send messages, so reading a channel
// requires a bot token against the REST API. Standard library only.
package discordbot

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"
)

// RestartCommand is the message text (case-insensitive) that triggers a
// restart.
const RestartCommand = "restart server"

// IsRestartCommand reports whether a message body is exactly the restart
// command. Matching is exact (after trimming) so messages merely mentioning
// a restart ("please don't restart server") do not trigger one.
func IsRestartCommand(content string) bool {
	return strings.EqualFold(strings.TrimSpace(content), RestartCommand)
}

// Message is the subset of a Discord channel message the bot needs.
type Message struct {
	ID      string `json:"id"`
	Content string `json:"content"`
	Author  struct {
		Bot bool `json:"bot"`
	} `json:"author"`
}

// Client talks to the Discord REST API with a bot token.
type Client struct {
	// APIBase defaults to the public Discord API; tests override it.
	APIBase   string
	Token     string
	ChannelID string
	HTTP      *http.Client
}

func (c *Client) apiBase() string {
	if c.APIBase != "" {
		return c.APIBase
	}
	return "https://discord.com/api/v10"
}

func (c *Client) httpClient() *http.Client {
	if c.HTTP != nil {
		return c.HTTP
	}
	return &http.Client{Timeout: 10 * time.Second}
}

func (c *Client) do(req *http.Request) (*http.Response, error) {
	req.Header.Set("Authorization", "Bot "+c.Token)
	return c.httpClient().Do(req)
}

// MessagesAfter returns the messages posted after message ID after, oldest
// first. An empty after returns the most recent messages of the channel.
func (c *Client) MessagesAfter(ctx context.Context, after string) ([]Message, error) {
	u := fmt.Sprintf("%s/channels/%s/messages?limit=100", c.apiBase(), c.ChannelID)
	if after != "" {
		u += "&after=" + after
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return nil, err
	}
	resp, err := c.do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("discord list messages: status %d", resp.StatusCode)
	}
	var msgs []Message
	if err := json.NewDecoder(resp.Body).Decode(&msgs); err != nil {
		return nil, fmt.Errorf("discord list messages: %w", err)
	}
	return msgs, nil
}

// Post sends a text message to the channel.
func (c *Client) Post(ctx context.Context, content string) error {
	u := fmt.Sprintf("%s/channels/%s/messages", c.apiBase(), c.ChannelID)
	body, err := json.Marshal(map[string]string{"content": content})
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, u, strings.NewReader(string(body)))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := c.do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 400 {
		return fmt.Errorf("discord post: status %d", resp.StatusCode)
	}
	return nil
}

// Watcher tracks the newest message seen and decides when a restart command
// fires. The first poll only establishes the baseline: messages that existed
// before the bot started never trigger a restart.
type Watcher struct {
	Client   *Client
	Cooldown time.Duration // minimum time between two restarts; 0 disables

	lastID   string
	armed    bool
	lastFire time.Time
}

// Poll fetches new messages once and reports whether a restart was requested.
// Bot authors are ignored so the bot's own confirmations cannot retrigger it.
func (w *Watcher) Poll(ctx context.Context) (bool, error) {
	msgs, err := w.Client.MessagesAfter(ctx, w.lastID)
	if err != nil {
		return false, err
	}
	if len(msgs) == 0 {
		w.armed = true
		return false, nil
	}
	// Messages arrive oldest first; the last one is the newest.
	w.lastID = msgs[len(msgs)-1].ID
	if !w.armed {
		w.armed = true
		return false, nil
	}
	for _, m := range msgs {
		if m.Author.Bot || !IsRestartCommand(m.Content) {
			continue
		}
		if w.Cooldown > 0 && time.Since(w.lastFire) < w.Cooldown {
			return false, nil
		}
		w.lastFire = time.Now()
		return true, nil
	}
	return false, nil
}
