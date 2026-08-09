package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func makeMap(t *testing.T, serverDir, name string) {
	t.Helper()
	dir := filepath.Join(serverDir, "media", "maps", name)
	if err := os.MkdirAll(dir, 0755); err != nil {
		t.Fatalf("MkdirAll(%s): %v", dir, err)
	}
	if err := os.WriteFile(filepath.Join(dir, "spawnpoints.lua"), []byte("-- spawnpoints\n"), 0644); err != nil {
		t.Fatalf("write spawnpoints: %v", err)
	}
}

func spawnTestCfg(t *testing.T) *ServerConfig {
	t.Helper()
	return &ServerConfig{
		ServerName:   "testworld",
		DataDir:      t.TempDir(),
		ServerDir:    t.TempDir(),
		SpawnRegions: "",
	}
}

func TestWriteSpawnRegionsEmptyNoop(t *testing.T) {
	cfg := spawnTestCfg(t)
	if err := cfg.WriteSpawnRegions(); err != nil {
		t.Fatalf("WriteSpawnRegions: %v", err)
	}
	if _, err := os.Stat(cfg.SpawnRegionsPath()); !os.IsNotExist(err) {
		t.Errorf("empty SPAWN_REGIONS must not write %s", cfg.SpawnRegionsPath())
	}
}

func TestWriteSpawnRegionsContent(t *testing.T) {
	cfg := spawnTestCfg(t)
	cfg.SpawnRegions = "West Point, KY;Rosewood, KY"
	makeMap(t, cfg.ServerDir, "Rosewood, KY")
	makeMap(t, cfg.ServerDir, "West Point, KY")

	if err := cfg.WriteSpawnRegions(); err != nil {
		t.Fatalf("WriteSpawnRegions: %v", err)
	}
	data, err := os.ReadFile(cfg.SpawnRegionsPath())
	if err != nil {
		t.Fatalf("read spawnregions: %v", err)
	}
	content := string(data)

	// Deterministic: regions sorted by name, single valid Lua table.
	if !strings.HasPrefix(content, "function SpawnRegions()") {
		t.Errorf("missing function header:\n%s", content)
	}
	if !strings.HasSuffix(strings.TrimSpace(content), "end") {
		t.Errorf("missing closing end:\n%s", content)
	}
	r1 := strings.Index(content, `"Rosewood, KY"`)
	r2 := strings.Index(content, `"West Point, KY"`)
	if r1 == -1 || r2 == -1 {
		t.Fatalf("missing regions:\n%s", content)
	}
	if r1 > r2 {
		t.Errorf("regions not sorted deterministically:\n%s", content)
	}
	if !strings.Contains(content, `file = "media/maps/Rosewood, KY/spawnpoints.lua"`) {
		t.Errorf("missing file path for Rosewood:\n%s", content)
	}
	if strings.Count(content, `{ name =`) != 2 {
		t.Errorf("expected exactly 2 regions:\n%s", content)
	}
}

func TestWriteSpawnRegionsDeterministic(t *testing.T) {
	cfg := spawnTestCfg(t)
	cfg.SpawnRegions = "Rosewood, KY;West Point, KY"
	makeMap(t, cfg.ServerDir, "Rosewood, KY")
	makeMap(t, cfg.ServerDir, "West Point, KY")

	if err := cfg.WriteSpawnRegions(); err != nil {
		t.Fatalf("WriteSpawnRegions: %v", err)
	}
	first, _ := os.ReadFile(cfg.SpawnRegionsPath())
	for i := 0; i < 3; i++ {
		if err := cfg.WriteSpawnRegions(); err != nil {
			t.Fatalf("WriteSpawnRegions: %v", err)
		}
		again, _ := os.ReadFile(cfg.SpawnRegionsPath())
		if string(first) != string(again) {
			t.Fatalf("spawnregions output not deterministic:\n%s\n---\n%s", first, again)
		}
	}
}

func TestWriteSpawnRegionsSkipsMissingMap(t *testing.T) {
	cfg := spawnTestCfg(t)
	// Rosewood exists, West Point does not -> only Rosewood is written.
	cfg.SpawnRegions = "Rosewood, KY;West Point, KY"
	makeMap(t, cfg.ServerDir, "Rosewood, KY")

	if err := cfg.WriteSpawnRegions(); err != nil {
		t.Fatalf("WriteSpawnRegions: %v", err)
	}
	data, _ := os.ReadFile(cfg.SpawnRegionsPath())
	content := string(data)
	if !strings.Contains(content, `"Rosewood, KY"`) {
		t.Errorf("missing existing region:\n%s", content)
	}
	if strings.Contains(content, `"West Point, KY"`) {
		t.Errorf("missing map must be skipped:\n%s", content)
	}
}

func TestWriteSpawnRegionsNoValidRegionLeavesFile(t *testing.T) {
	cfg := spawnTestCfg(t)
	cfg.SpawnRegions = "Rosewood, KY"
	if err := cfg.WriteSpawnRegions(); err != nil {
		t.Fatalf("WriteSpawnRegions: %v", err)
	}
	if _, err := os.Stat(cfg.SpawnRegionsPath()); !os.IsNotExist(err) {
		t.Errorf("no valid region must not write %s", cfg.SpawnRegionsPath())
	}

	// A previously generated file must not be clobbered by a broken config.
	if err := os.MkdirAll(filepath.Dir(cfg.SpawnRegionsPath()), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(cfg.SpawnRegionsPath(), []byte("keep me\n"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := cfg.WriteSpawnRegions(); err != nil {
		t.Fatalf("WriteSpawnRegions: %v", err)
	}
	data, _ := os.ReadFile(cfg.SpawnRegionsPath())
	if string(data) != "keep me\n" {
		t.Errorf("existing spawnregions clobbered by invalid config: %q", data)
	}
}

func TestParseSpawnRegions(t *testing.T) {
	got := parseSpawnRegions("Rosewood, KY;West Point, KY;Rosewood, KY")
	if len(got) != 2 || got[0] != "Rosewood, KY" || got[1] != "West Point, KY" {
		t.Errorf("parseSpawnRegions = %v, want [Rosewood, KY West Point, KY]", got)
	}
	// Commas inside a name must not split entries.
	if got := parseSpawnRegions("Muldraugh, KY"); len(got) != 1 || got[0] != "Muldraugh, KY" {
		t.Errorf("comma-containing name split: %v", got)
	}
	// Invalid characters are rejected.
	for _, bad := range []string{`Rosewood"; os.execute("rm")`, "Rosewood/KY"} {
		if got := parseSpawnRegions(bad); len(got) != 0 {
			t.Errorf("parseSpawnRegions(%q) = %v, want empty", bad, got)
		}
	}
	// Newlines separate entries.
	if got := parseSpawnRegions("Rosewood\nWest Point"); len(got) != 2 || got[0] != "Rosewood" || got[1] != "West Point" {
		t.Errorf("parseSpawnRegions with newline = %v, want [Rosewood West Point]", got)
	}
	// Surrounding quotes (from quoted .env values) are stripped.
	for _, raw := range []string{`"Rosewood, KY"`, `'West Point, KY'`} {
		if got := parseSpawnRegions(raw); len(got) != 1 {
			t.Errorf("parseSpawnRegions(%q) = %v, want 1 entry", raw, got)
		}
	}
}
