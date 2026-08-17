// discordbot watches a Discord channel for the "restart server" command and
// restarts the game server by recreating its compose service on the latest
// image. It runs as a sidecar with the docker socket mounted; the game
// server container itself stays socket-free.
package main

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	"github.com/tibuski/project-zomboid-server-docker/internal/discordbot"
)

func envStr(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}

func envDuration(key string, def time.Duration) time.Duration {
	v := os.Getenv(key)
	if v == "" {
		return def
	}
	d, err := time.ParseDuration(v)
	if err != nil {
		fmt.Printf("WARNING: invalid %s %q, using %s\n", key, v, def)
		return def
	}
	return d
}

// Seam for tests, following the package-level override pattern used across
// this codebase.
var runCompose = func(ctx context.Context, dir string, args ...string) error {
	base := []string{
		"compose",
		"-f", filepath.Join(dir, "docker-compose.yml"),
		"--project-directory", dir,
	}
	cmd := exec.CommandContext(ctx, "docker", append(base, args...)...)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	return cmd.Run()
}

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGTERM, syscall.SIGINT)
	defer stop()

	token := os.Getenv("DISCORD_BOT_TOKEN")
	channel := os.Getenv("DISCORD_CHANNEL_ID")
	if token == "" || channel == "" {
		// Idle instead of exiting so the service can stay enabled in compose
		// without crash-looping when the bot is not configured.
		fmt.Println("DISCORD_BOT_TOKEN/DISCORD_CHANNEL_ID not set; restart bot disabled (idling)")
		<-ctx.Done()
		return
	}

	var (
		composeDir = envStr("COMPOSE_DIR", "/compose")
		service    = envStr("RESTART_SERVICE", "zomboid")
		publicName = envStr("PUBLIC_NAME", "the server")
		pollEvery  = envDuration("POLL_INTERVAL", 5*time.Second)
		cooldown   = envDuration("RESTART_COOLDOWN", 5*time.Minute)
	)

	client := &discordbot.Client{Token: token, ChannelID: channel, APIBase: os.Getenv("DISCORD_API_BASE")}
	watcher := &discordbot.Watcher{Client: client, Cooldown: cooldown}
	fmt.Printf("Watching Discord channel %s for %q (service %s, cooldown %s)\n",
		channel, discordbot.RestartCommand, service, cooldown)

	for {
		select {
		case <-ctx.Done():
			return
		case <-time.After(pollEvery):
		}

		restart, err := watcher.Poll(ctx)
		if err != nil {
			fmt.Printf("Discord poll failed: %v\n", err)
			continue
		}
		if restart {
			restartServer(ctx, client, composeDir, service, publicName)
		}
	}
}

// restartServer recreates the game service on the latest image. It cannot be
// "compose down && up": down would stop this sidecar mid-command. Pulling and
// force-recreating just the game service is the equivalent for it.
func restartServer(ctx context.Context, client *discordbot.Client, dir, service, publicName string) {
	say := func(msg string) {
		if err := client.Post(ctx, msg); err != nil {
			fmt.Printf("Discord post failed: %v\n", err)
		}
	}

	fmt.Println("Restart requested via Discord")
	say(fmt.Sprintf("🔄 Restarting **%s**… (pulling the latest image first)", publicName))

	if err := runCompose(ctx, dir, "pull", service); err != nil {
		fmt.Printf("Image pull failed: %v\n", err)
		say(fmt.Sprintf("⚠️ Restart of **%s** aborted: image pull failed.", publicName))
		return
	}
	if err := runCompose(ctx, dir, "up", "-d", "--force-recreate", service); err != nil {
		fmt.Printf("Service recreate failed: %v\n", err)
		say(fmt.Sprintf("⚠️ Restart of **%s** failed while recreating the container.", publicName))
		return
	}
	say(fmt.Sprintf("✅ **%s** is booting on the latest image.", publicName))
}
