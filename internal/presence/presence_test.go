package presence

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"
)

type recordingNotifier struct {
	joined []string
	left   []struct{ name, steamID string }
	done   chan struct{}
}

func (r *recordingNotifier) PlayerJoined(name string) {
	r.joined = append(r.joined, name)
	r.done <- struct{}{}
}

func (r *recordingNotifier) PlayerLeft(name, steamID string) {
	r.left = append(r.left, struct{ name, steamID string }{name, steamID})
	r.done <- struct{}{}
}

func writeUserLog(t *testing.T, dir, name, content string) string {
	t.Helper()
	path := filepath.Join(dir, name)
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}
	return path
}

func waitEvent(t *testing.T, r *recordingNotifier) {
	t.Helper()
	select {
	case <-r.done:
	case <-time.After(5 * time.Second):
		t.Fatal("timed out waiting for notification")
	}
}

// The exact *_user.txt lines produced by the PZ build 42.20.2 server.
const sampleLog = `[09-08-26 11:13:59.039] Connection add index=0 guid=1786274014518790 id=null.
[09-08-26 11:14:00.638] 76561197995809551 "tibus" attempting to join.
[09-08-26 11:14:00.642] 76561197995809551 "tibus" allowed to join.
[09-08-26 11:16:08.385] Connection disconnect index=0 guid=1786274014518790 id=76561197995809551.
[09-08-26 11:16:08.387] Connection remove index=0 guid=1786274014518790 id=76561197995809551.
`

func TestNewestUserLog(t *testing.T) {
	dir := t.TempDir()
	_ = writeUserLog(t, dir, "logs_2026-08-09/2026-08-09_10-47_user.txt", "")
	newer := writeUserLog(t, dir, "logs_2026-08-09/2026-08-09_10-51_user.txt", "")
	_ = writeUserLog(t, dir, "2026-08-09_11-18_chat.txt", "")

	got := newestUserLog(dir)
	if got != newer {
		t.Errorf("newestUserLog = %q, want %q", got, newer)
	}
}

func TestNewestUserLogEmpty(t *testing.T) {
	if got := newestUserLog(t.TempDir()); got != "" {
		t.Errorf("newestUserLog = %q, want empty", got)
	}
}

func TestParseJoinLeave(t *testing.T) {
	r := &recordingNotifier{done: make(chan struct{}, 16)}
	tailer := NewTailer(t.TempDir(), r)

	for _, line := range []string{
		`[09-08-26 11:14:00.642] 76561197995809551 "tibus" allowed to join.`,
		`[09-08-26 11:16:08.385] Connection disconnect index=0 guid=1786274014518790 id=76561197995809551.`,
	} {
		tailer.process(line)
	}

	if len(r.joined) != 1 || r.joined[0] != "tibus" {
		t.Errorf("joined = %v, want [tibus]", r.joined)
	}
	if len(r.left) != 1 || r.left[0].name != "tibus" || r.left[0].steamID != "76561197995809551" {
		t.Errorf("left = %+v, want tibus/76561197995809551", r.left)
	}
}

func TestParseIgnoresNonPresenceLines(t *testing.T) {
	r := &recordingNotifier{done: make(chan struct{}, 16)}
	tailer := NewTailer(t.TempDir(), r)

	for _, line := range []string{
		`[09-08-26 11:13:59.039] Connection add index=0 guid=1786274014518790 id=null.`,
		`[09-08-26 11:14:00.638] 76561197995809551 "tibus" attempting to join.`,
		`[09-08-26 11:16:08.387] Connection remove index=0 guid=1786274014518790 id=76561197995809551.`,
		`Connection disconnect index=0 guid=1786274014518790 id=null.`,
		`12:00:00.000 some other log line`,
	} {
		tailer.process(line)
	}

	if len(r.joined) != 0 || len(r.left) != 0 {
		t.Errorf("unexpected notifications: joined=%v left=%+v", r.joined, r.left)
	}
}

func TestParseNameOnlyJoin(t *testing.T) {
	r := &recordingNotifier{done: make(chan struct{}, 16)}
	tailer := NewTailer(t.TempDir(), r)

	tailer.process(`[09-08-26 11:14:00.642] "Bob" allowed to join.`)

	if len(r.joined) != 1 || r.joined[0] != "Bob" {
		t.Errorf("joined = %v, want [Bob]", r.joined)
	}
}

func TestParseDisconnectUnknownPlayer(t *testing.T) {
	r := &recordingNotifier{done: make(chan struct{}, 16)}
	tailer := NewTailer(t.TempDir(), r)

	// No matching join was seen, so the name resolves to empty.
	tailer.process(`Connection disconnect index=0 guid=1786274014518790 id=76561197995809551.`)

	if len(r.left) != 1 || r.left[0].name != "" || r.left[0].steamID != "76561197995809551" {
		t.Errorf("left = %+v, want empty name/76561197995809551", r.left)
	}
}

// End-to-end: the tailer attaches to the newest file, skips history without
// notifying, then reports joins/leaves appended afterwards.
func TestRunTailsAppendedEvents(t *testing.T) {
	dir := t.TempDir()
	path := writeUserLog(t, dir, "logs_2026-08-09/2026-08-09_10-51_user.txt",
		`[09-08-26 11:14:00.642] 76561197995809551 "tibus" allowed to join.
`)
	r := &recordingNotifier{done: make(chan struct{}, 16)}
	tailer := NewTailer(dir, r)
	tailer.PollInterval = 10 * time.Millisecond

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go tailer.Run(ctx)

	// Give the tailer a moment to attach (and skip history).
	time.Sleep(50 * time.Millisecond)
	if len(r.joined) != 0 {
		t.Fatalf("history replayed: joined=%v", r.joined)
	}

	f, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0644)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.WriteString(`[09-08-26 11:16:08.385] Connection disconnect index=0 guid=1786274014518790 id=76561197995809551.
`); err != nil {
		t.Fatal(err)
	}
	f.Close()

	waitEvent(t, r)
	if len(r.joined) != 0 {
		t.Errorf("unexpected join events: %v", r.joined)
	}
	if len(r.left) != 1 || r.left[0].name != "tibus" || r.left[0].steamID != "76561197995809551" {
		t.Errorf("left = %+v, want tibus/76561197995809551", r.left)
	}
}

func TestRunPrimeProvidesNameForPriorJoin(t *testing.T) {
	dir := t.TempDir()
	path := writeUserLog(t, dir, "2026-08-09_10-51_user.txt", sampleLog)
	r := &recordingNotifier{done: make(chan struct{}, 16)}
	tailer := NewTailer(dir, r)
	tailer.PollInterval = 10 * time.Millisecond

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go tailer.Run(ctx)

	// History is not replayed but the name map is primed: a leave appended now
	// resolves to "tibus" even though the join was before we attached.
	time.Sleep(50 * time.Millisecond)

	f, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0644)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.WriteString(`[09-08-26 11:18:00.000] Connection disconnect index=1 guid=1786274014518791 id=76561197995809551.
`); err != nil {
		t.Fatal(err)
	}
	f.Close()

	waitEvent(t, r)
	if len(r.left) != 1 || r.left[0].name != "tibus" {
		t.Errorf("left = %+v, want name tibus", r.left)
	}
}
