package presence

import (
	"context"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"
)

// Notifier receives player presence events. The Discord webhook adapter in
// cmd/entrypoint implements it; keeping the interface here avoids a dependency
// from this package on internal/webhook.
type Notifier interface {
	PlayerJoined(name string)
	PlayerLeft(name, steamID string)
}

// Tailer tails the newest *_user.txt under a PZ data dir's Logs folder and
// reports player joins and leaves. PZ logs each session into timestamped
// *_user.txt files; only the newest one is being written. The tailer re-selects
// it whenever a new session starts, primes the steamID->name map from the
// existing lines, then only reports events appended after it attached.
type Tailer struct {
	LogsDir      string
	PollInterval time.Duration
	Notify       Notifier

	steamIDName map[string]string
}

func NewTailer(logsDir string, notify Notifier) *Tailer {
	return &Tailer{
		LogsDir:      logsDir,
		PollInterval: 2 * time.Second,
		Notify:       notify,
		steamIDName:  map[string]string{},
	}
}

var (
	// "76561197995809551 \"tibus\" allowed to join."
	joinRe = regexp.MustCompile(`(?:([0-9]{15,17}) )?"([^"]+)" allowed to join\.?`)
	// "Connection disconnect index=0 guid=1786274014518790 id=76561197995809551."
	// The leading space keeps the match from landing on the guid's "id=".
	leaveRe = regexp.MustCompile(`Connection disconnect .*? id=([0-9]{15,17})\.?`)
)

// Run tails until ctx is cancelled. It never returns an error; parse and I/O
// failures are skipped so a flaky log file cannot take the entrypoint down.
func (t *Tailer) Run(ctx context.Context) {
	var (
		path   string
		file   *os.File
		offset int64
	)

	attach := func(p string) {
		if file != nil {
			file.Close()
		}
		f, err := os.Open(p)
		if err != nil {
			file = nil
			path = ""
			return
		}
		file = f
		path = p
		t.prime(f)
		// Report only events that happen after we attached.
		offset, _ = f.Seek(0, io.SeekEnd)
	}

	for {
		if newest := newestUserLog(t.LogsDir); newest != "" && newest != path {
			attach(newest)
		}

		if file != nil {
			t.drain(file, &offset)
		}

		select {
		case <-ctx.Done():
			if file != nil {
				file.Close()
			}
			return
		case <-time.After(t.PollInterval):
		}
	}
}

// prime scans the whole file once to build the steamID->name map from "allowed
// to join" lines without notifying, so a disconnect for a player who joined
// before we attached still resolves to a name.
func (t *Tailer) prime(f *os.File) {
	if _, err := f.Seek(0, io.SeekStart); err != nil {
		return
	}
	data, err := io.ReadAll(f)
	if err != nil {
		return
	}
	for _, line := range strings.Split(string(data), "\n") {
		line = strings.TrimSpace(line)
		if m := joinRe.FindStringSubmatch(line); m != nil && m[1] != "" {
			t.steamIDName[m[1]] = m[2]
		}
	}
}

// drain reads new lines appended since offset and processes them, advancing
// offset. If the file was truncated or replaced it re-primes and skips ahead.
func (t *Tailer) drain(f *os.File, offset *int64) {
	info, err := f.Stat()
	if err != nil {
		return
	}
	if info.Size() < *offset {
		t.prime(f)
		*offset, _ = f.Seek(0, io.SeekEnd)
		return
	}
	buf := make([]byte, info.Size()-*offset)
	if _, err := f.ReadAt(buf, *offset); err != nil {
		return
	}
	*offset = info.Size()
	for _, line := range strings.Split(string(buf), "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		t.process(line)
	}
}

func (t *Tailer) process(line string) {
	if m := joinRe.FindStringSubmatch(line); m != nil {
		// The steamID is optional (older builds logged name-only lines).
		if m[1] != "" {
			t.steamIDName[m[1]] = m[2]
		}
		t.Notify.PlayerJoined(m[2])
		return
	}
	if m := leaveRe.FindStringSubmatch(line); m != nil {
		name := t.steamIDName[m[1]]
		delete(t.steamIDName, m[1])
		t.Notify.PlayerLeft(name, m[1])
	}
}

// newestUserLog returns the most recently modified *_user.txt under logsDir,
// or "" when there is none. mtime picks the live session: PZ keeps writing to
// the current session's file while archived ones stay untouched.
func newestUserLog(logsDir string) string {
	var (
		bestPath string
		bestTime time.Time
	)
	_ = filepath.WalkDir(logsDir, func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() || !strings.HasSuffix(d.Name(), "_user.txt") {
			return nil
		}
		info, err := d.Info()
		if err != nil {
			return nil
		}
		if info.ModTime().Before(bestTime) {
			return nil
		}
		// Equal mtimes (coarse filesystems) fall back to lexical order: the
		// timestamped names sort newest last, which is also the session.
		bestPath, bestTime = p, info.ModTime()
		return nil
	})
	return bestPath
}
