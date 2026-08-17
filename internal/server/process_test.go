package server

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/tibuski/project-zomboid-server-docker/internal/config"
)

func TestJavaOptions(t *testing.T) {
	cfg := config.DefaultConfig()
	cfg.MaxRam = "8192m"
	cfg.MinRam = "2048m"
	cfg.GCConfig = "G1"
	cfg.JvmExtraArgs = "-Dfoo=bar -XX:MaxGCPauseMillis=200"

	m := NewManager(cfg)
	opts := m.javaOptions()
	for _, want := range []string{"-Xms2048m", "-Xmx8192m", "-XX:+UseG1GC", "-Dfoo=bar"} {
		if !strings.Contains(opts, want) {
			t.Errorf("javaOptions = %q, missing %q", opts, want)
		}
	}
}

func TestJavaOptionsEmptyExtra(t *testing.T) {
	cfg := config.DefaultConfig()
	m := NewManager(cfg)
	opts := m.javaOptions()
	if strings.Contains(opts, "UseZGCGC") {
		t.Errorf("GC flag mangled: %q", opts)
	}
	if !strings.Contains(opts, "-XX:+UseZGC") {
		t.Errorf("default GC not applied: %q", opts)
	}
}

func writeStartScript(t *testing.T, dir string) {
	t.Helper()
	script := filepath.Join(dir, "start-server.sh")
	// Sleep in the background so the wrapper stays alive like the real
	// server process and the java child is part of the process group.
	content := "#!/bin/bash\nsleep 100 &\nwait\n"
	if err := os.WriteFile(script, []byte(content), 0755); err != nil {
		t.Fatal(err)
	}
}

func newTestManager(t *testing.T) *Manager {
	t.Helper()
	dir := t.TempDir()
	writeStartScript(t, dir)

	cfg := config.DefaultConfig()
	cfg.ServerDir = dir
	cfg.DataDir = dir
	cfg.AdminPassword = "admin-pass"
	cfg.RCONPassword = "rcon-pass"
	cfg.TZ = "UTC"
	cfg.RCONPort = 1 // RCON connect will fail fast; Stop falls back to SIGTERM

	return NewManager(cfg)
}

func TestStopTerminatesProcessGroup(t *testing.T) {
	m := newTestManager(t)
	if err := m.Start(); err != nil {
		t.Fatalf("Start: %v", err)
	}
	if m.PID() == 0 {
		t.Fatal("PID is 0")
	}

	// RCON is unreachable, so Stop must fall back to killing the process group.
	if err := m.Stop(); err != nil {
		t.Fatalf("Stop: %v", err)
	}

	// Wait must return promptly once the process is gone.
	done := make(chan error, 1)
	go func() { done <- m.Wait() }()
	select {
	case <-done:
	case <-time.After(10 * time.Second):
		t.Fatal("Wait did not return after Stop")
	}
}

func TestStopBeforeStart(t *testing.T) {
	m := newTestManager(t)
	if err := m.Stop(); err != nil {
		t.Fatalf("Stop before Start should be a no-op, got %v", err)
	}
}

func TestStartPassesAdminPassword(t *testing.T) {
	dir := t.TempDir()
	argsFile := filepath.Join(dir, "args.txt")
	script := filepath.Join(dir, "start-server.sh")
	content := fmt.Sprintf("#!/bin/bash\necho \"$@\" > %s\n", argsFile)
	if err := os.WriteFile(script, []byte(content), 0755); err != nil {
		t.Fatal(err)
	}

	cfg := config.DefaultConfig()
	cfg.ServerDir = dir
	cfg.DataDir = dir
	cfg.AdminPassword = "admin-pass"
	cfg.UseSteam = true

	m := NewManager(cfg)
	if err := m.Start(); err != nil {
		t.Fatalf("Start: %v", err)
	}
	if err := m.Wait(); err != nil {
		t.Fatalf("Wait: %v", err)
	}

	data, err := os.ReadFile(argsFile)
	if err != nil {
		t.Fatal(err)
	}
	args := string(data)
	if !strings.Contains(args, "-servername servertest") {
		t.Errorf("args = %q, want -servername servertest", args)
	}
	if !strings.Contains(args, "-adminpassword admin-pass") {
		t.Errorf("args = %q, want -adminpassword admin-pass", args)
	}
}

func TestBootWatcher(t *testing.T) {
	fired := 0
	w := newBootWatcher(func() { fired++ })

	// A marker split across writes must still match, and a repeat of the
	// marker must not fire the hook twice.
	for _, chunk := range []string{
		"LOG  : Network     , 1754900000000> RCON: listening on",
		" port 27015\n",
		"some other line\n",
		"RCON: listening on port 27015\n",
	} {
		if _, err := io.WriteString(w, chunk); err != nil {
			t.Fatal(err)
		}
	}
	if fired != 1 {
		t.Errorf("hook fired %d times, want 1", fired)
	}
}

func TestBootWatcherNoMatch(t *testing.T) {
	fired := 0
	w := newBootWatcher(func() { fired++ })
	if _, err := io.WriteString(w, "LOG  : General, 1> server booting\n"); err != nil {
		t.Fatal(err)
	}
	if fired != 0 {
		t.Errorf("hook fired %d times, want 0", fired)
	}
}

// End-to-end: a server whose stdout carries the boot marker fires OnBoot.
func TestStartFiresOnBoot(t *testing.T) {
	dir := t.TempDir()
	script := filepath.Join(dir, "start-server.sh")
	content := "#!/bin/bash\necho 'RCON: listening on port 27015'\n"
	if err := os.WriteFile(script, []byte(content), 0755); err != nil {
		t.Fatal(err)
	}

	cfg := config.DefaultConfig()
	cfg.ServerDir = dir
	cfg.DataDir = dir
	cfg.AdminPassword = "admin-pass"

	m := NewManager(cfg)
	booted := make(chan struct{}, 1)
	m.OnBoot = func() { booted <- struct{}{} }
	if err := m.Start(); err != nil {
		t.Fatalf("Start: %v", err)
	}

	select {
	case <-booted:
	case <-time.After(5 * time.Second):
		t.Fatal("OnBoot did not fire")
	}
	if err := m.Wait(); err != nil {
		t.Fatalf("Wait: %v", err)
	}
}
