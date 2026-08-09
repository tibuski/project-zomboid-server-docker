package config

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
)

// validSpawnRegionRE restricts SPAWN_REGIONS entries to names safe to write
// into a Lua string literal and to use in a path under media/maps/. Map folder
// names legitimately contain commas and spaces (e.g. "Rosewood, KY"), so only
// quotes, backslashes and other separators are rejected.
var validSpawnRegionRE = regexp.MustCompile(`^[A-Za-z0-9 ,-]+$`)

// SpawnRegionsPath returns the per-server spawnregions.lua, the file that
// lists the regions offered on the character-creation spawn screen.
func (c *ServerConfig) SpawnRegionsPath() string {
	return c.DataDir + "/Server/" + c.ServerName + "_spawnregions.lua"
}

// parseSpawnRegions splits a SPAWN_REGIONS value into region names. Entries
// are separated by ';' (or whitespace/newlines) because the names themselves
// contain commas ("Rosewood, KY"). Duplicates and invalid names are dropped.
func parseSpawnRegions(raw string) []string {
	var regions []string
	seen := map[string]struct{}{}
	for _, part := range strings.FieldsFunc(raw, func(r rune) bool {
		return r == ';' || r == '\n' || r == '\t'
	}) {
		name := strings.TrimSpace(part)
		if name == "" {
			continue
		}
		if !validSpawnRegionRE.MatchString(name) {
			fmt.Printf("WARNING: SPAWN_REGIONS entry %q contains invalid characters, ignoring (allowed: letters, digits, spaces, ',' and '-'; separate entries with ';')\n", name)
			continue
		}
		if _, ok := seen[name]; ok {
			continue
		}
		seen[name] = struct{}{}
		regions = append(regions, name)
	}
	return regions
}

// WriteSpawnRegions writes the per-server spawnregions.lua so only the maps
// listed in SPAWN_REGIONS are offered on the spawn screen. It is a no-op when
// SPAWN_REGIONS is empty, letting the server keep its generated file. Regions
// without a spawnpoints.lua in the installed server files are skipped; the
// file is only written when at least one valid region remains, so a typo never
// leaves the server without any spawnable region.
func (c *ServerConfig) WriteSpawnRegions() error {
	if strings.TrimSpace(c.SpawnRegions) == "" {
		return nil
	}

	valid := make([]string, 0, 4)
	for _, name := range parseSpawnRegions(c.SpawnRegions) {
		if !spawnpointsExists(c.ServerDir, name) {
			fmt.Printf("WARNING: SPAWN_REGIONS entry %q has no media/maps/%s/spawnpoints.lua in the server files, ignoring\n", name, name)
			continue
		}
		valid = append(valid, name)
	}
	if len(valid) == 0 {
		fmt.Println("WARNING: no valid SPAWN_REGIONS entries, leaving spawnregions.lua untouched")
		return nil
	}
	sort.Strings(valid)

	var sb strings.Builder
	sb.WriteString("function SpawnRegions()\n")
	sb.WriteString("\treturn {\n")
	for _, name := range valid {
		sb.WriteString(fmt.Sprintf("\t\t{ name = %q, file = %q },\n", name, "media/maps/"+name+"/spawnpoints.lua"))
	}
	sb.WriteString("\t}\n")
	sb.WriteString("end\n")

	if err := os.MkdirAll(filepath.Dir(c.SpawnRegionsPath()), 0755); err != nil {
		return fmt.Errorf("creating spawnregions directory: %w", err)
	}
	if err := os.WriteFile(c.SpawnRegionsPath(), []byte(sb.String()), 0644); err != nil {
		return fmt.Errorf("writing spawnregions.lua: %w", err)
	}
	fmt.Printf("Spawn regions written (%s)\n", strings.Join(valid, ", "))
	return nil
}

func spawnpointsExists(serverDir, name string) bool {
	info, err := os.Stat(filepath.Join(serverDir, "media", "maps", name, "spawnpoints.lua"))
	return err == nil && !info.IsDir()
}
