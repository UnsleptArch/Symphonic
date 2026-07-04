package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"
)

// Result is the v1.1 "state awareness" record for a single tool run.
// Deliberately minimal — exit code, log path, timing, ran/not. No output
// parsing, no signal extraction, no scoring. That's v1.2+, and building
// it before this metadata layer exists would mean parsing logic with
// nowhere consistent to put its findings.
//
// Signals is included now as an empty slice so the JSON shape is stable
// once v1.2 starts populating it — avoids a schema change later just to
// add a field that was always going to be needed.
type Result struct {
	Tool      string   `json:"tool"`
	Target    string   `json:"target"`
	Ran       bool     `json:"ran"`
	ExitCode  int      `json:"exit_code"`
	LogFile   string   `json:"log_file"`
	Timestamp int64    `json:"timestamp"`
	ErrMsg    string   `json:"error,omitempty"`
	Signals   []string `json:"signals"`
}

// newResult stamps the current time automatically so call sites don't
// have to remember to.
func newResult(tool, target string) Result {
	return Result{
		Tool:      tool,
		Target:    target,
		Timestamp: time.Now().Unix(),
		Signals:   []string{},
	}
}

// writeResults dumps the full run's results as a single JSON file
// (results.json) inside outDir, alongside the per-tool .log files.
// One file per run, not one file per tool — keeps it simple to load
// later for whatever v1.2's parsing layer ends up looking like.
func writeResults(outDir string, results []Result) error {
	path := filepath.Join(outDir, "results.json")
	f, err := os.Create(path)
	if err != nil {
		return fmt.Errorf("could not create results.json: %w", err)
	}
	defer f.Close()

	enc := json.NewEncoder(f)
	enc.SetIndent("", "  ")
	if err := enc.Encode(results); err != nil {
		return fmt.Errorf("could not write results.json: %w", err)
	}
	return nil
}
