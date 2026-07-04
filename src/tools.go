package main

import (
	"net/url"
	"os/exec"
	"strings"
)

// wordlistPath points at the small built-in wordlist shipped alongside
// the binary. Swap this out for something bigger (e.g. SecLists) later —
// this is intentionally minimal so v1 works out of the box with zero
// extra setup.
const wordlistPath = "wordlist.txt"

// defaultFlags is the starting point used when conf.yaml's flags: block
// doesn't have an entry for a given tool. These are just reasonable
// defaults, not enforced limits — override any of them in conf.yaml.
// {target} and {domain} get substituted before the string is split into
// argv below.
var defaultFlags = map[string]string{
	"subfinder": "-d {domain} -silent",
	"katana":    "-u {target} -silent -depth 3",
	"httpx":     "-u {target} -silent -tech-detect -status-code",
	"ffuf":      "-u {target}/FUZZ -w " + wordlistPath + " -mc 200,301,302,401,403 -rate 10 -s",
	"arjun":     "-u {target}",
	"nuclei":    "-u {target} -silent -tags exposure,misconfig,default-login,tech",
	"dalfox":    "url {target} --skip-bav",
	"sqlmap":    "-u {target} --batch --level=1 --risk=1",
}

// toolOrder is the fixed execution order for v1. Sequential, on purpose —
// no concurrency yet, keeps the rate-limit story simple and honest.
//
// Order follows recon -> probe -> confirm:
//   subfinder, katana, httpx, ffuf, arjun   = recon/surface mapping
//   nuclei, dalfox, sqlmap                  = confirm/vuln-class detection
var toolOrder = []string{"subfinder", "katana", "httpx", "ffuf", "arjun", "nuclei", "dalfox", "sqlmap"}

// bareDomain strips scheme/path/port from a target URL so tools that
// expect a plain domain (subfinder) get one, instead of choking on a
// full URL. Falls back to returning the input unchanged if parsing fails.
func bareDomain(target string) string {
	u, err := url.Parse(target)
	if err != nil || u.Hostname() == "" {
		return target
	}
	return u.Hostname()
}

// buildCommand looks up the flag string for a tool (conf.yaml override,
// falling back to defaultFlags), substitutes {target}/{domain}, splits
// it into argv, and returns the exec.Cmd. This is a naive whitespace
// split — if you need a flag value containing a literal space, this
// won't handle quoting, worth knowing before you rely on it.
func buildCommand(tool string, target string, cfg *Config) *exec.Cmd {
	flagStr, ok := cfg.ToolFlags[tool]
	if !ok || flagStr == "" {
		flagStr, ok = defaultFlags[tool]
		if !ok {
			return nil
		}
	}

	flagStr = strings.ReplaceAll(flagStr, "{target}", target)
	flagStr = strings.ReplaceAll(flagStr, "{domain}", bareDomain(target))

	args := strings.Fields(flagStr)
	return exec.Command(tool, args...)
}
