// signals.go — v1.2 "start understanding output, not just storing it."
//
// Scope is deliberately limited to 3 tools: httpx, ffuf, nuclei. dalfox
// and sqlmap are NOT covered here — dalfox's output would need its own
// pass later, and sqlmap has no clean structured mode worth trusting
// (see the note in tools.go). Adding more tools to this file is fine,
// but do it one at a time and actually check the JSON shape against
// your installed version rather than assuming it matches what's below.
//
// None of this does anything with the findings beyond turning them into
// short "signal" strings attached to a Result. No decisions get made
// off of these yet — that's v1.3 (conditional execution).
package main

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"
)

// extractSignals reads a tool's output file and returns signal strings.
// Returns nil for anything outside the v1.2 scope — that's expected,
// not an error.
func extractSignals(tool, outputFile string) []string {
	data, err := os.ReadFile(outputFile)
	if err != nil {
		return nil
	}

	switch tool {
	case "httpx":
		return extractHTTPXSignals(data)
	case "ffuf":
		return extractFFUFSignals(data)
	case "nuclei":
		return extractNucleiSignals(data)
	default:
		return nil
	}
}

// httpx -json emits one JSON object per line (JSONL).
type httpxLine struct {
	URL        string   `json:"url"`
	StatusCode int      `json:"status_code"`
	Tech       []string `json:"tech"`
	Title      string   `json:"title"`
}

func extractHTTPXSignals(data []byte) []string {
	var signals []string
	for _, line := range strings.Split(string(data), "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		var h httpxLine
		if err := json.Unmarshal([]byte(line), &h); err != nil {
			continue // not JSON, or a version-mismatch field shape — skip rather than crash
		}
		if h.StatusCode != 0 {
			signals = append(signals, fmt.Sprintf("status:%d", h.StatusCode))
		}
		for _, t := range h.Tech {
			signals = append(signals, "tech:"+t)
		}
	}
	return signals
}

// ffuf -of json -o <file> writes one JSON object with a "results" array.
type ffufOutput struct {
	Results []struct {
		URL    string `json:"url"`
		Status int    `json:"status"`
	} `json:"results"`
}

func extractFFUFSignals(data []byte) []string {
	var out ffufOutput
	if err := json.Unmarshal(data, &out); err != nil {
		return nil
	}
	var signals []string
	for _, r := range out.Results {
		signals = append(signals, fmt.Sprintf("endpoint_found:%s:%d", r.URL, r.Status))
	}
	return signals
}

// nuclei -jsonl emits one JSON object per line, one per matched finding.
type nucleiLine struct {
	TemplateID string `json:"template-id"`
	Info       struct {
		Name     string `json:"name"`
		Severity string `json:"severity"`
	} `json:"info"`
	MatchedAt string `json:"matched-at"`
}

func extractNucleiSignals(data []byte) []string {
	var signals []string
	for _, line := range strings.Split(string(data), "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		var n nucleiLine
		if err := json.Unmarshal([]byte(line), &n); err != nil {
			continue
		}
		signals = append(signals, fmt.Sprintf("finding:%s:%s", n.TemplateID, n.Info.Severity))
	}
	return signals
}
