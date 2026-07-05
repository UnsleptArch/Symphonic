package main

import (
	"bufio"
	"fmt"
	"os"
	"strconv"
	"strings"
)

// Config is the flat structure we expect out of conf.yaml.
type Config struct {
	ConsentConfirmed bool
	Target           string
	RateLimitSeconds int
	Tools            map[string]bool
	// ToolFlags holds the raw flag string for a tool if the user set one
	// under a "flags:" block in conf.yaml. If a tool has no entry here,
	// defaultFlags[tool] (see tools.go) is used instead. Nothing in this
	// program inspects or blocks what goes in here — that's on you.
	ToolFlags map[string]string

	// AllowedDomains is a SCAFFOLD field, parsed but not enforced by
	// anything yet. It exists now so v1.3's conditional/chained execution
	// (e.g. "subfinder found a new subdomain, auto-scan it") has
	// somewhere to check against before it exists. If you never build
	// v1.3, this field just sits unused — harmless either way.
	AllowedDomains []string

	// Plugins enable/disable map, same true/false pattern as Tools.
	// Each key is a plugin directory name under PluginsDir.
	Plugins        map[string]bool
	PluginsEnabled bool
	PluginsDir     string
}

// loadConfig parses a deliberately tiny subset of YAML — just what
// conf.yaml actually needs. Top-level "key: value" pairs, plus nested
// blocks ("tools:", "flags:", "plugins:") each holding 2-space-indented
// "name: value" lines, and one list block ("allowed_domains:") holding
// 2-space-indented "- value" lines. This is NOT a general YAML parser.
// Don't reuse it for anything else.
func loadConfig(path string) (*Config, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("could not open config %s: %w", path, err)
	}
	defer f.Close()

	cfg := &Config{
		Tools:     map[string]bool{},
		ToolFlags: map[string]string{},
		Plugins:   map[string]bool{},
	}
	section := "" // "" | "tools" | "flags" | "plugins" | "allowed_domains"

	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		rawLine := scanner.Text()
		trimmed := strings.TrimSpace(rawLine)

		if trimmed == "" || strings.HasPrefix(trimmed, "#") {
			continue
		}

		switch trimmed {
		case "tools:":
			section = "tools"
			continue
		case "flags:":
			section = "flags"
			continue
		case "plugins:":
			section = "plugins"
			continue
		case "allowed_domains:":
			section = "allowed_domains"
			continue
		}

		isIndented := strings.HasPrefix(rawLine, "  ") || strings.HasPrefix(rawLine, "\t")
		if section != "" && isIndented {
			if section == "allowed_domains" {
				if strings.HasPrefix(trimmed, "- ") {
					val := strings.Trim(strings.TrimSpace(strings.TrimPrefix(trimmed, "-")), `"' `)
					if val != "" {
						cfg.AllowedDomains = append(cfg.AllowedDomains, val)
					}
				}
				continue
			}

			parts := strings.SplitN(trimmed, ":", 2)
			if len(parts) == 2 {
				key := strings.TrimSpace(parts[0])
				val := strings.TrimSpace(parts[1])
				switch section {
				case "tools":
					cfg.Tools[key] = strings.EqualFold(val, "true")
				case "flags":
					// Keep the raw string as-is (minus surrounding quotes) —
					// this is passed straight through to the tool later.
					cfg.ToolFlags[key] = strings.Trim(val, `"'`)
				case "plugins":
					cfg.Plugins[key] = strings.EqualFold(val, "true")
				}
			}
			continue
		}

		// Any non-indented line ends whichever nested block we were in.
		section = ""

		parts := strings.SplitN(trimmed, ":", 2)
		if len(parts) != 2 {
			continue
		}
		key := strings.TrimSpace(parts[0])
		val := strings.Trim(strings.TrimSpace(parts[1]), `"'`)

		switch key {
		case "consent_confirmed":
			cfg.ConsentConfirmed = strings.EqualFold(val, "true")
		case "target":
			cfg.Target = val
		case "rate_limit_seconds":
			n, convErr := strconv.Atoi(val)
			if convErr == nil {
				cfg.RateLimitSeconds = n
			}
		case "plugins_enabled":
			cfg.PluginsEnabled = strings.EqualFold(val, "true")
		case "plugins_dir":
			cfg.PluginsDir = val
		}
	}

	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("error reading config: %w", err)
	}
	if cfg.PluginsDir == "" {
		cfg.PluginsDir = "plugins"
	}
	return cfg, nil
}
