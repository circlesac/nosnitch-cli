// nosnitch — is your coding agent snitching your code to model training?
//
// Reads local Codex CLI creds and your browser's ChatGPT session to report,
// per account, whether a setting lets your data reach OpenAI model training.
package main

import (
	"encoding/json"
	"fmt"
	"os"

	"github.com/circlesac/nosnitch-cli/internal/chatgpt"
	"github.com/circlesac/nosnitch-cli/internal/codex"
)

var version = "0.0.0-dev"

func main() {
	cmd := "check"
	if len(os.Args) > 1 {
		cmd = os.Args[1]
	}
	switch cmd {
	case "check":
		asJSON := false
		for _, a := range os.Args[2:] {
			if a == "--json" || a == "-json" {
				asJSON = true
			}
		}
		os.Exit(runCheck(asJSON))
	case "version", "-v", "--version":
		fmt.Println("nosnitch", version)
	case "help", "-h", "--help":
		usage()
	default:
		fmt.Fprintf(os.Stderr, "unknown command: %s\n\n", cmd)
		usage()
		os.Exit(2)
	}
}

func usage() {
	fmt.Print(`nosnitch — is your coding agent snitching your code to model training?

Usage:
  nosnitch check [--json]   Check Codex CLI + ChatGPT web accounts
  nosnitch version
  nosnitch help

Exit: 0 = clean, 1 = a training-share setting is ON, 2 = indeterminate
`)
}

func runCheck(asJSON bool) int {
	cx := codex.Check()
	web := chatgpt.Check()

	if asJSON {
		out, _ := json.MarshalIndent(map[string]any{
			"codex_cli": cx, "chatgpt_web": web,
		}, "", "  ")
		fmt.Println(string(out))
		return exitCode(cx, web)
	}

	printReport(cx, web)
	return exitCode(cx, web)
}

func exitCode(cx codex.Result, web chatgpt.Result) int {
	if cx.Risk() || web.Risk() {
		return 1
	}
	if !cx.OK && !web.OK {
		return 2
	}
	return 0
}

// ── pretty report ──

const (
	red, grn, yel, dim, bold, rst = "\033[31m", "\033[32m", "\033[33m", "\033[2m", "\033[1m", "\033[0m"
)

func c(s, color string) string {
	if !isTTY() || os.Getenv("NO_COLOR") != "" {
		return s
	}
	return color + s + rst
}

func isTTY() bool {
	fi, err := os.Stdout.Stat()
	return err == nil && fi.Mode()&os.ModeCharDevice != 0
}

func flag(v *bool) string {
	switch {
	case v == nil:
		return c("?", yel)
	case *v:
		return c("ON", red)
	default:
		return c("OFF", grn)
	}
}

func printReport(cx codex.Result, web chatgpt.Result) {
	fmt.Println(c("nosnitch — coding-agent training-share check", bold))
	fmt.Println()

	fmt.Println(c("[codex-cli]", bold), c("~/.codex/auth.json", dim))
	if cx.OK {
		fmt.Printf("  account            : %s  (ChatGPT %s)\n", cx.Email, cx.Plan)
		fmt.Printf("  API data-sharing   : %s  %s\n", boolFlag(cx.APIDataSharing),
			c("(incentives program = API-key traffic collected for training)", dim))
		if cx.APIDataSharing {
			fmt.Println(c("     → turn off: platform.openai.com/settings/organization/data-controls/sharing", yel))
		}
	} else {
		fmt.Println(c("  skipped: "+cx.Reason, yel))
	}
	fmt.Println()

	fmt.Println(c("[chatgpt-web]", bold), c("Chrome session", dim))
	if web.OK {
		fmt.Printf("  account                  : %s\n", web.Email)
		fmt.Printf("  Improve the model (all)  : %s\n", flag(web.TrainingAllowed))
		fmt.Printf("  Codex content training   : %s\n", flag(web.CodexTrainingAllowed))
	} else {
		fmt.Println(c("  skipped: "+web.Reason, yel))
	}
	fmt.Println()

	if cx.OK && web.OK && cx.Email != web.Email {
		fmt.Println(c(fmt.Sprintf("⚠️  Codex CLI account (%s) ≠ browser account (%s) — "+
			"the chatgpt-web result is for a different account than Codex uses.",
			cx.Email, web.Email), yel))
		fmt.Println()
	}

	if cx.Risk() || web.Risk() {
		fmt.Println(c("verdict: ⚠️  a training-share setting is ON — review above", red))
	} else if !cx.OK && !web.OK {
		fmt.Println(c("verdict: could not determine (both checks skipped)", yel))
	} else {
		fmt.Println(c("verdict: ✅ no training-share detected", grn))
	}
}

func boolFlag(b bool) string {
	if b {
		return c("ON", red)
	}
	return c("OFF", grn)
}
