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
}

// loadConfig parses a deliberately tiny subset of YAML — just what
// conf.yaml actually needs. Top-level "key: value" pairs, plus two
// nested blocks ("tools:" and "flags:") each holding 2-space-indented
// "name: value" lines. This is NOT a general YAML parser. Don't reuse
// it for anything else.
func loadConfig(path string) (*Config, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("could not open config %s: %w", path, err)
	}
	defer f.Close()

	cfg := &Config{Tools: map[string]bool{}, ToolFlags: map[string]string{}}
	section := "" // "" | "tools" | "flags"

	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		rawLine := scanner.Text()
		trimmed := strings.TrimSpace(rawLine)

		if trimmed == "" || strings.HasPrefix(trimmed, "#") {
			continue
		}

		if trimmed == "tools:" {
			section = "tools"
			continue
		}
		if trimmed == "flags:" {
			section = "flags"
			continue
		}

		isIndented := strings.HasPrefix(rawLine, "  ") || strings.HasPrefix(rawLine, "\t")
		if section != "" && isIndented {
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
		}
	}

	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("error reading config: %w", err)
	}
	return cfg, nil
}
