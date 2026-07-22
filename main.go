// nosnitch — stop your coding agent from snitching your code to model training.
//
//	nosnitch          show what's exposed (status)
//	nosnitch off      opt out — turn training/data-sharing OFF
//	nosnitch status   same as no args
package main

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"strings"

	"github.com/circlesac/nosnitch-cli/internal/account"
	"github.com/circlesac/nosnitch-cli/internal/chatgpt"
	"github.com/circlesac/nosnitch-cli/internal/cookies"
)

var version = "0.0.0-dev"

const fdaSettingsURL = "x-apple.systempreferences:com.apple.preference.security?Privacy_AllFiles"

func main() {
	cmd := "status"
	if len(os.Args) > 1 {
		cmd = os.Args[1]
	}
	switch cmd {
	case "status", "check":
		os.Exit(runStatus(hasFlag("--json")))
	case "off":
		os.Exit(runOff())
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

func hasFlag(name string) bool {
	if len(os.Args) < 3 {
		return false
	}
	for _, a := range os.Args[2:] {
		if a == name {
			return true
		}
	}
	return false
}

func usage() {
	fmt.Print(`nosnitch — stop your coding agent from snitching your code to model training.

Usage:
  nosnitch            Show what's exposed to training (status)
  nosnitch off        Opt out — turn training/data-sharing OFF
  nosnitch status     Same as no args   (--json for machine output)
  nosnitch version

Status exit code: 0 = clean, 1 = a training-share setting is ON, 2 = indeterminate
`)
}

func runStatus(asJSON bool) int {
	rep := account.Gather()
	if asJSON {
		out, _ := json.MarshalIndent(rep, "", "  ")
		fmt.Println(string(out))
		return statusCode(rep)
	}
	printStatus(rep)
	return statusCode(rep)
}

func statusCode(rep account.Report) int {
	if rep.Risk() {
		return 1
	}
	if len(rep.Accounts) == 0 {
		return 2
	}
	return 0
}

func runOff() int {
	fmt.Println(c("nosnitch", bold), c("· opting out of model training…", dim))
	fmt.Println()

	acted, fdaBlocked := false, false
	for _, b := range cookies.Installed() {
		jar, err := b.ChatGPT()
		if err == cookies.ErrNeedFullDiskAccess {
			fdaBlocked = true
			continue
		}
		if err != nil || jar == nil {
			continue
		}
		r := chatgpt.OffWith(jar)
		if !r.OK {
			continue
		}
		acted = true
		fmt.Printf("  %s  %s\n", c(r.Email, bold), c("via "+b.Name, dim))
		for _, f := range chatgpt.TrainingFeatures {
			state := r.Results[f.Key]
			col := grn
			if state != "off" {
				col = red
			}
			field(f.Label, state, col, "")
		}
		fmt.Println()
	}

	if fdaBlocked {
		fmt.Println(c("  ! Safari session found but couldn't be read — needs Full Disk Access.", yel))
		fmt.Println(c("    Opening the setting; add your terminal, then re-run `nosnitch off`.", dim))
		exec.Command("open", fdaSettingsURL).Run()
	}

	if !acted {
		if fdaBlocked {
			return 2
		}
		fmt.Println(c("  no readable ChatGPT session found", yel))
		return 2
	}
	fmt.Println(c("  ✓ opted out — these accounts' data won't be used for training", grn))
	return 0
}

// ── report ──

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

// field prints one aligned row:  label      VALUE   note
func field(label, value, valueColor, note string) {
	const w = 18
	pad := w - len([]rune(label))
	if pad < 1 {
		pad = 1
	}
	line := fmt.Sprintf("    %s%s%s", c(label, dim), spaces(pad), c(value, valueColor))
	if note != "" {
		line += "   " + c(note, dim)
	}
	fmt.Println(line)
}

func capitalize(s string) string {
	if s == "" {
		return s
	}
	return strings.ToUpper(s[:1]) + s[1:]
}

func spaces(n int) string {
	b := make([]byte, n)
	for i := range b {
		b[i] = ' '
	}
	return string(b)
}

func flagRow(label string, v *bool, onNote string) {
	if v == nil {
		return // unknown from this source set — omit rather than clutter
	}
	if *v {
		field(label, "ON", yel, onNote)
	} else {
		field(label, "OFF", grn, "")
	}
}

func printStatus(rep account.Report) {
	fmt.Println(c("nosnitch", bold), c("· is OpenAI training on your code?", dim))
	fmt.Println()

	for _, a := range rep.Accounts {
		fmt.Println("  " + c(a.Email, bold))
		if a.Plan != "" {
			field("Plan", "ChatGPT "+capitalize(a.Plan), "", "")
		}
		field("Signed in", strings.Join(a.Sources, ", "), "", "")
		flagRow("API data sharing", a.APIDataSharing, "API traffic used for training")
		for _, f := range chatgpt.TrainingFeatures {
			flagRow(f.Label, a.Training[f.Key], f.OnNote)
		}
		fmt.Println()
	}

	for _, b := range rep.Blocked {
		fmt.Println(c("  ! "+b.Source+" — "+b.Reason+" to read its session.", yel))
		fmt.Println(c(`    open "`+fdaSettingsURL+`"  (add your terminal, then re-run)`, dim))
		fmt.Println()
	}

	switch {
	case rep.Risk():
		fmt.Println(c("  ✗ training-share is ON", red), c("— run `nosnitch off` to opt out", dim))
	case len(rep.Accounts) == 0:
		fmt.Println(c("  ? indeterminate", yel), c("— no ChatGPT session found", dim))
	default:
		fmt.Println(c("  ✓ nothing is exposed to training", grn))
	}
}
