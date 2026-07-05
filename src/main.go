// Symphonic v1.2 — orchestrator + state-awareness layer + signal
// extraction + Python plugin system.
//
// What this does, in order:
//   1. Loads conf.yaml (config.go)
//   2. Refuses to run at all unless consent_confirmed: true is set
//   3. Loads any plugins under PluginsDir (plugins.go), runs before_run
//      hooks
//   4. Runs each enabled tool ONE AT A TIME (no concurrency yet) against
//      the target, using flags from conf.yaml's "flags:" block, or a
//      sane default if the user didn't set one for that tool (tools.go)
//   5. For httpx/ffuf/nuclei, parses the tool's structured output into
//      short "signal" strings (signals.go). dalfox/sqlmap stay log-only.
//   6. Runs any after_tool:<name> plugin hooks for that tool
//   7. Writes results.json, runs after_run plugin hooks, prints a summary
//
// No conditional execution, no correlation, no scoring, no bandit yet.
// That's v1.3+.
//
// FOSS project, no enforcement: defaultFlags in tools.go are sane
// starting points, not a ceiling. Anything in conf.yaml's flags: block
// replaces them entirely for that tool — this program does not inspect,
// filter, or block particular flag values.
//
// Core Symphonic will never ship or maintain RCE-class or DDoS/load-
// class tooling in-tree. If that's ever wanted, it belongs in an
// external, unreviewed plugin — not this repo. Plugins are unsandboxed
// subprocesses by design; see plugins.go for why.
package main

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"time"
)

func main() {
	confPath := "conf.yaml"
	if len(os.Args) > 1 {
		confPath = os.Args[1]
	}

	cfg, err := loadConfig(confPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "failed to load config: %v\n", err)
		os.Exit(1)
	}

	// Consent gate. This is the whole gate for v1: one explicit boolean
	// the user has to set themselves. No signing, no scope schema yet —
	// that's a later iteration. But this check is non-negotiable even
	// in the simplest version.
	if !cfg.ConsentConfirmed {
		fmt.Fprintln(os.Stderr, "Refusing to run: consent_confirmed is not set to true in conf.yaml.")
		fmt.Fprintln(os.Stderr, "Only run this against targets you own or have explicit written authorization to test.")
		os.Exit(1)
	}

	if cfg.Target == "" {
		fmt.Fprintln(os.Stderr, "Refusing to run: no target set in conf.yaml.")
		os.Exit(1)
	}

	if cfg.RateLimitSeconds < 1 {
		fmt.Fprintln(os.Stderr, "rate_limit_seconds not set or invalid, defaulting to 2s between tools.")
		cfg.RateLimitSeconds = 2
	}

	outDir := filepath.Join("output", time.Now().Format("20060102-150405"))
	if err := os.MkdirAll(outDir, 0o755); err != nil {
		fmt.Fprintf(os.Stderr, "could not create output dir: %v\n", err)
		os.Exit(1)
	}

	fmt.Printf("Symphonic v1.2 — target: %s\n", cfg.Target)
	fmt.Printf("Output directory: %s\n\n", outDir)

	var manifests []PluginManifest
	if cfg.PluginsEnabled {
		manifests = loadPlugins(cfg.PluginsDir)
		fmt.Printf("Plugins loaded: %d (from %s)\n\n", len(manifests), cfg.PluginsDir)
	}

	var results []Result

	pluginCtx := PluginContext{
		Target:    cfg.Target,
		Domain:    bareDomain(cfg.Target),
		OutputDir: outDir,
		Results:   results,
	}
	results = append(results, runPluginsForHook("before_run", pluginCtx, manifests, cfg, outDir)...)

	for i, tool := range toolOrder {
		enabled, ok := cfg.Tools[tool]
		if !ok || !enabled {
			continue
		}

		r := newResult(tool, cfg.Target)

		jsonOutPath := ""
		if tool == "ffuf" {
			jsonOutPath = filepath.Join(outDir, tool+".json")
		}

		cmd := buildCommand(tool, cfg.Target, cfg, jsonOutPath)
		if cmd == nil {
			r.ErrMsg = "unknown tool"
			results = append(results, r)
			continue
		}

		outFile := filepath.Join(outDir, tool+".log")
		f, err := os.Create(outFile)
		if err != nil {
			r.ErrMsg = fmt.Sprintf("could not create log file: %v", err)
			results = append(results, r)
			continue
		}

		cmd.Stdout = f
		cmd.Stderr = f

		fmt.Printf("[%d/%d] running %s...\n", i+1, len(toolOrder), tool)
		runErr := cmd.Run()
		f.Close()

		if runErr != nil {
			if exitErr, isExitErr := runErr.(*exec.ExitError); isExitErr {
				r.Ran = true
				r.ExitCode = exitErr.ExitCode()
				r.LogFile = outFile
			} else {
				// Binary not found or failed to start at all.
				r.ErrMsg = fmt.Sprintf("failed to start (is it installed and on PATH?): %v", runErr)
				results = append(results, r)
				continue
			}
		} else {
			r.Ran = true
			r.ExitCode = 0
			r.LogFile = outFile
		}

		// v1.2 signal extraction — ffuf's structured output lives in its
		// own {output} JSON file, httpx/nuclei write JSONL to stdout
		// (already captured in outFile). dalfox/sqlmap return nil here,
		// which is expected, not an error.
		signalFile := outFile
		if tool == "ffuf" {
			signalFile = jsonOutPath
		}
		r.Signals = extractSignals(tool, signalFile)

		results = append(results, r)

		pluginCtx.Results = results
		results = append(results, runPluginsForHook("after_tool:"+tool, pluginCtx, manifests, cfg, outDir)...)

		// Simple global throttle between tool invocations. This is NOT
		// per-request rate limiting inside each tool — sqlmap/nuclei/etc
		// each have their own internal request pacing. This just makes
		// sure Symphonic itself doesn't launch tool after tool back to
		// back with zero gap. Good enough for v1, not a substitute for
		// per-request throttling later.
		if i != len(toolOrder)-1 {
			time.Sleep(time.Duration(cfg.RateLimitSeconds) * time.Second)
		}
	}

	pluginCtx.Results = results
	results = append(results, runPluginsForHook("after_run", pluginCtx, manifests, cfg, outDir)...)

	if err := writeResults(outDir, results); err != nil {
		fmt.Fprintf(os.Stderr, "warning: %v\n", err)
	}

	fmt.Println("\n--- Summary ---")
	for _, r := range results {
		if !r.Ran {
			fmt.Printf("%-8s FAILED TO RUN — %s\n", r.Tool, r.ErrMsg)
			continue
		}
		fmt.Printf("%-8s exit=%d  signals=%d  log=%s\n", r.Tool, r.ExitCode, len(r.Signals), r.LogFile)
	}
	fmt.Printf("\nStructured results: %s\n", filepath.Join(outDir, "results.json"))
}
