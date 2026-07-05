
// plugins.go — Python-based plugin system.
//
// Plugins are unsandboxed subprocesses. That's intentional, not an
// oversight: core Symphonic will never ship or maintain RCE-class or
// DDoS/load-class tooling in-tree, and this is the door for anyone who
// wants that themselves — a plugin runs with exactly the privileges you
// already have on your own machine, same as if you ran the script
// yourself. Only load plugins you wrote or fully trust. This program
// does not audit, sandbox, or restrict what a plugin does.
//
// Protocol, deliberately simple:
//   - each plugin lives in its own directory under PluginsDir — and can
//     only execute code from inside that directory. resolveEntrypoint
//     checks this at load time (absolute paths and ".." traversal in
//     plugin.json's entrypoint field are rejected before anything runs)
//   - that directory has a plugin.json manifest: {name, entrypoint, hooks}
//   - Symphonic invokes `python3 <entrypoint>` and writes a JSON
//     PluginContext to its stdin, then closes stdin
//   - the plugin writes a JSON PluginOutput to its stdout before exiting
//   - anything the plugin writes to stderr is captured to its own log
//     file, same as core tools get
//
// "Contained" means the entrypoint file can't live outside its plugin
// folder — it does NOT mean the running plugin process is sandboxed.
// Once python3 starts executing that file, it has the same privileges
// you do; it can still read/write anywhere on disk, hit the network,
// etc. Containment here is about which code gets to run, not what that
// code is allowed to do once running.
package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// PluginManifest is plugin.json's shape.
type PluginManifest struct {
	Name       string   `json:"name"`
	Entrypoint string   `json:"entrypoint"`
	Hooks      []string `json:"hooks"`
	Dir        string   `json:"-"` // absolute path to the plugin's own directory, filled in by loadPlugins
	// EntrypointPath is the validated, absolute path to the entrypoint
	// file, set by resolveEntrypoint during loadPlugins. runPlugin uses
	// this rather than re-joining Dir+Entrypoint itself, so there's a
	// single choke point where containment gets checked.
	EntrypointPath string `json:"-"`
}

// PluginContext is what gets written to a plugin's stdin as JSON.
type PluginContext struct {
	Hook      string   `json:"hook"`
	Target    string   `json:"target"`
	Domain    string   `json:"domain"`
	OutputDir string   `json:"output_dir"`
	Results   []Result `json:"results"`
}

// PluginOutput is what a plugin is expected to write to stdout as JSON.
// AdditionalResults get appended to the run's results list. Everything
// else about what the plugin did lives in its own log file — Symphonic
// doesn't try to interpret plugin behavior beyond this.
type PluginOutput struct {
	AdditionalResults []Result `json:"results"`
}

// loadPlugins scans pluginsDir for subdirectories containing a
// plugin.json. A directory with no manifest, a malformed one, or one
// whose entrypoint tries to escape its own plugin directory, is skipped
// with a warning rather than failing the whole run — one broken or
// malicious plugin manifest shouldn't take down the rest of the scan,
// and it shouldn't get to run at all either.
func loadPlugins(pluginsDir string) []PluginManifest {
	var manifests []PluginManifest

	entries, err := os.ReadDir(pluginsDir)
	if err != nil {
		return manifests // plugins dir just doesn't exist yet — not an error
	}

	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		pluginDir := filepath.Join(pluginsDir, entry.Name())
		manifestPath := filepath.Join(pluginDir, "plugin.json")
		data, err := os.ReadFile(manifestPath)
		if err != nil {
			continue // no manifest in this directory, skip silently
		}

		var m PluginManifest
		if err := json.Unmarshal(data, &m); err != nil {
			fmt.Fprintf(os.Stderr, "warning: could not parse %s: %v\n", manifestPath, err)
			continue
		}
		if m.Name == "" {
			m.Name = entry.Name()
		}
		m.Dir = pluginDir

		resolved, err := resolveEntrypoint(pluginDir, m.Entrypoint)
		if err != nil {
			fmt.Fprintf(os.Stderr, "warning: skipping plugin %q: %v\n", m.Name, err)
			continue
		}
		m.EntrypointPath = resolved

		manifests = append(manifests, m)
	}

	return manifests
}

// resolveEntrypoint enforces that a plugin's entrypoint file actually
// lives inside its own plugin directory. This is what makes "plugins
// only run from a contained plugins folder" true structurally, rather
// than just being a rule stated in a comment: an absolute path, or a
// relative path using ".." to climb out of pluginDir, is rejected here
// before anything gets executed — not caught later, not just discouraged.
func resolveEntrypoint(pluginDir, entrypoint string) (string, error) {
	if entrypoint == "" {
		return "", fmt.Errorf("no entrypoint set in plugin.json")
	}
	if filepath.IsAbs(entrypoint) {
		return "", fmt.Errorf("entrypoint must be a relative path, got absolute: %s", entrypoint)
	}

	absDir, err := filepath.Abs(pluginDir)
	if err != nil {
		return "", fmt.Errorf("could not resolve plugin directory: %w", err)
	}
	absEntry, err := filepath.Abs(filepath.Join(absDir, entrypoint))
	if err != nil {
		return "", fmt.Errorf("could not resolve entrypoint path: %w", err)
	}

	// absEntry must be inside absDir — not equal to it (that'd mean no
	// filename at all) and not able to escape via "..".
	rel, err := filepath.Rel(absDir, absEntry)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(os.PathSeparator)) {
		return "", fmt.Errorf("entrypoint escapes its plugin directory: %s", entrypoint)
	}

	if _, err := os.Stat(absEntry); err != nil {
		return "", fmt.Errorf("entrypoint file not found: %s", absEntry)
	}

	return absEntry, nil
}

// hookMatches checks whether a manifest declares interest in a given
// hook name (exact string match — "after_tool:httpx" only matches that
// literal string, not a wildcard).
func hookMatches(m PluginManifest, hook string) bool {
	for _, h := range m.Hooks {
		if h == hook {
			return true
		}
	}
	return false
}

// runPluginsForHook runs every enabled plugin that declares the given
// hook, in the order loadPlugins found them (directory read order —
// not guaranteed stable across filesystems, worth knowing if plugin
// run order ever matters to you). Returns any Results the plugins
// reported, and logs plugin stderr/failures to pluginLogDir.
func runPluginsForHook(hook string, ctx PluginContext, manifests []PluginManifest, cfg *Config, pluginLogDir string) []Result {
	var collected []Result

	if !cfg.PluginsEnabled {
		return collected
	}

	for _, m := range manifests {
		if !hookMatches(m, hook) {
			continue
		}
		enabled, ok := cfg.Plugins[m.Name]
		if !ok || !enabled {
			continue // plugins must be explicitly enabled in conf.yaml, same as tools:
		}

		ctx.Hook = hook
		out, err := runPlugin(m, ctx, pluginLogDir)
		if err != nil {
			fmt.Fprintf(os.Stderr, "plugin %s (%s) failed: %v\n", m.Name, hook, err)
			continue
		}
		collected = append(collected, out.AdditionalResults...)
	}

	return collected
}

// runPlugin invokes a single plugin's entrypoint as a subprocess,
// feeding it the JSON context on stdin and expecting JSON back on
// stdout. Plugin stderr goes to its own log file under pluginLogDir.
// Uses m.EntrypointPath, which was already validated as contained
// within m.Dir back in loadPlugins — no path logic happens here.
func runPlugin(m PluginManifest, ctx PluginContext, pluginLogDir string) (PluginOutput, error) {
	var out PluginOutput

	if m.EntrypointPath == "" {
		return out, fmt.Errorf("plugin %s has no validated entrypoint (this shouldn't happen — loadPlugins should have filtered it out)", m.Name)
	}

	ctxJSON, err := json.Marshal(ctx)
	if err != nil {
		return out, fmt.Errorf("could not marshal plugin context: %w", err)
	}

	cmd := exec.Command("python3", m.EntrypointPath)
	cmd.Stdin = bytes.NewReader(ctxJSON)

	var stdout bytes.Buffer
	cmd.Stdout = &stdout

	logPath := filepath.Join(pluginLogDir, "plugin-"+m.Name+"-"+sanitizeHookForFilename(ctx.Hook)+".log")
	logFile, err := os.Create(logPath)
	if err == nil {
		cmd.Stderr = logFile
		defer logFile.Close()
	}

	if err := cmd.Run(); err != nil {
		return out, fmt.Errorf("subprocess error: %w", err)
	}

	if stdout.Len() == 0 {
		// A plugin that doesn't print anything back isn't necessarily
		// broken — maybe it only has side effects. Just return empty.
		return out, nil
	}

	if err := json.Unmarshal(stdout.Bytes(), &out); err != nil {
		return out, fmt.Errorf("could not parse plugin stdout as JSON: %w", err)
	}
	return out, nil
}

func sanitizeHookForFilename(hook string) string {
	// "after_tool:httpx" -> "after_tool_httpx", keeps filenames clean.
	result := make([]byte, 0, len(hook))
	for _, c := range []byte(hook) {
		if c == ':' {
			result = append(result, '_')
		} else {
			result = append(result, c)
		}
	}
	return string(result)
}
