// nosnitch checks and clears AI account training and public-sharing exposure.
//
//	nosnitch                         show account privacy status
//	nosnitch off                     clear all detected exposure
//	nosnitch <provider> training     turn off one provider's training
//	nosnitch claude unshare          remove only public Claude links
package main

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"strings"

	"github.com/circlesac/nosnitch-cli/internal/account"
	"github.com/circlesac/nosnitch-cli/internal/chatgpt"
	"github.com/circlesac/nosnitch-cli/internal/claude"
	"github.com/circlesac/nosnitch-cli/internal/cookies"
)

func main() {
	cmd := "status"
	if len(os.Args) > 1 {
		cmd = os.Args[1]
	}
	switch cmd {
	case "status", "check":
		if hasFlag("-h") || hasFlag("--help") {
			usage()
			return
		}
		os.Exit(runStatus(hasFlag("--json")))
	case "off":
		if hasFlag("-h") || hasFlag("--help") {
			usage()
			return
		}
		os.Exit(runOff(hasFlag("--yes")))
	case "openai":
		os.Exit(runProviderCommand("openai"))
	case "claude":
		os.Exit(runProviderCommand("anthropic"))
	case "version", "-v", "--version":
		fmt.Println("nosnitch", Version)
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
	fmt.Print(`nosnitch — check AI account training and public sharing settings.

Usage:
  nosnitch [check|status] [--json]
      Show account training and public-sharing exposure.

  nosnitch off [--yes]
      Turn off supported training settings and remove public Claude links.
      Confirms before changing account state unless --yes is supplied.

  nosnitch openai training [--yes]
      Turn off OpenAI Account training settings.

  nosnitch claude training [--yes]
      Turn off Claude Account model improvement.

  nosnitch claude unshare [--yes]
      Remove public Claude links without changing training settings.

  nosnitch version

Check exit codes:
  0  clean
  1  training or public-sharing exposure found
  2  incomplete — one or more account checks could not be completed
`)
}

func runProviderCommand(provider string) int {
	label := "OpenAI"
	if provider == "anthropic" {
		label = "Claude"
	}
	if len(os.Args) < 3 {
		fmt.Fprintf(os.Stderr, "missing %s command\n\n", label)
		providerUsage(provider, os.Stderr)
		return 2
	}
	if os.Args[2] == "help" || hasFlag("-h") || hasFlag("--help") {
		providerUsage(provider, os.Stdout)
		return 0
	}
	if os.Args[2] == "training" {
		if provider == "openai" {
			return runOpenAIOff(hasFlag("--yes"))
		}
		return runClaudeTrainingOff(hasFlag("--yes"))
	}
	if os.Args[2] == "unshare" && provider == "anthropic" {
		return runUnshare(hasFlag("--yes"))
	}
	fmt.Fprintf(os.Stderr, "unknown %s command: %s\n\n", label, os.Args[2])
	providerUsage(provider, os.Stderr)
	return 2
}

func providerUsage(provider string, out *os.File) {
	if provider == "anthropic" {
		fmt.Fprint(out, `Usage:
  nosnitch claude training [--yes]
      Turn off Claude Account model improvement.

  nosnitch claude unshare [--yes]
      Remove public Claude links without changing training settings.
`)
		return
	}
	fmt.Fprint(out, `Usage:
  nosnitch openai training [--yes]
      Turn off OpenAI Account training settings.
`)
}

type claudeSession struct {
	email  string
	source string
	jar    map[string]string
	shares []claude.SharedConversation
}

func discoverClaudeSessions() []claudeSession {
	var sessions []claudeSession
	seenAccounts := map[string]bool{}
	for _, browser := range cookies.Installed() {
		jar, err := browser.Claude()
		if err != nil || jar == nil {
			continue
		}
		result := claude.CheckWeb(jar)
		if !result.OK || seenAccounts[result.Email] {
			continue
		}
		seenAccounts[result.Email] = true
		if len(result.SharedConversations) > 0 {
			sessions = append(sessions, claudeSession{
				email: result.Email, source: browser.Name, jar: jar,
				shares: result.SharedConversations,
			})
		}
	}
	return sessions
}

func runUnshare(yes bool) int {
	sessions := discoverClaudeSessions()
	if len(sessions) == 0 {
		fmt.Println(c("  ✓ no shared Claude chats found", grn))
		return 0
	}

	total := 0
	for _, current := range sessions {
		fmt.Println("  " + c("[Claude Account]", bold))
		field("Account", current.email, "", "")
		field("Discovered via", current.source, "", "")
		for _, shared := range current.shares {
			total++
			printSharedChat(shared)
		}
		fmt.Println()
	}
	if !yes {
		fmt.Printf("Remove %d public Claude share link(s)? [y/N] ", total)
		answer, _ := bufio.NewReader(os.Stdin).ReadString('\n')
		answer = strings.TrimSpace(strings.ToLower(answer))
		if answer != "y" && answer != "yes" {
			fmt.Println("Cancelled.")
			return 0
		}
	}

	removed, failed := 0, 0
	for _, current := range sessions {
		result := claude.UnshareWith(current.jar, current.shares)
		removed += len(result.Removed)
		failed += len(result.Failed)
	}
	fmt.Printf("  %s\n", c(fmt.Sprintf("✓ removed %d public Claude share link(s)", removed), grn))
	if failed > 0 {
		fmt.Printf("  %s\n", c(fmt.Sprintf("✗ failed to remove %d link(s)", failed), red))
		return 1
	}
	return 0
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
	if rep.Indeterminate() {
		return 2
	}
	return 0
}

type offOutcome struct {
	acted         bool
	failed        bool
	indeterminate bool
	cancelled     bool
}

func runOff(yes bool) int {
	fmt.Println(c("nosnitch", bold), c("· reviewing all account privacy exposure…", dim))
	fmt.Println()
	claudeOutcome := turnOffClaude(yes)
	if claudeOutcome.cancelled {
		return 0
	}
	openAIOutcome := turnOffOpenAI("nosnitch off")
	return finishOff(mergeOutcomes(claudeOutcome, openAIOutcome),
		"training and public-sharing exposure turned off")
}

func runOpenAIOff(yes bool) int {
	if !yes && !confirm("Turn off OpenAI Account training settings?") {
		return 0
	}
	fmt.Println(c("nosnitch", bold), c("· turning off OpenAI Account training…", dim))
	fmt.Println()
	return finishOff(turnOffOpenAI("nosnitch openai training"),
		"OpenAI Account training turned off")
}

func runClaudeTrainingOff(yes bool) int {
	if !yes && !confirm("Turn off Claude Account model improvement?") {
		return 0
	}
	fmt.Println(c("nosnitch", bold), c("· turning off Claude Account training…", dim))
	fmt.Println()
	result := claude.OffCode()
	if !result.OK {
		if result.Email == "" {
			fmt.Println(c("  no Claude Code account could be updated", yel))
			return 2
		}
		fmt.Println(c("  ✗ Claude model improvement: "+result.Reason, red))
		return 1
	}
	fmt.Println("  " + c("[Claude Account]", bold))
	field("Account", result.Email, "", "")
	field("Discovered via", "Claude Code", "", "")
	field("Model improvement", "OFF", grn, "")
	fmt.Println()
	fmt.Println(c("  ✓ Claude Account training turned off", grn))
	return 0
}

func turnOffClaude(yes bool) offOutcome {
	claudeSessions := discoverClaudeSessions()
	sharedCount := 0
	for _, current := range claudeSessions {
		sharedCount += len(current.shares)
	}
	if !yes {
		if sharedCount > 0 {
			fmt.Println(c("Public Claude links to remove:", yel))
			for _, current := range claudeSessions {
				for _, shared := range current.shares {
					printSharedChat(shared)
				}
			}
			fmt.Println()
		}
		prompt := "Turn off all supported training settings?"
		if sharedCount > 0 {
			prompt = fmt.Sprintf("Turn off all supported training settings and remove %d public link(s)?", sharedCount)
		}
		if !confirm(prompt) {
			return offOutcome{cancelled: true}
		}
	}

	outcome := offOutcome{}
	claudeOff := claude.OffCode()
	if claudeOff.OK {
		outcome.acted = true
		fmt.Println("  " + c("[Claude Account]", bold))
		field("Account", claudeOff.Email, "", "")
		field("Discovered via", "Claude Code", "", "")
		field("Model improvement", "OFF", grn, "")
		fmt.Println()
	} else if claudeOff.Email != "" {
		outcome.failed = true
		fmt.Println(c("  ! Claude model improvement: "+claudeOff.Reason, yel))
	} else {
		outcome.indeterminate = true
	}

	removed, unshareFailed := 0, 0
	for _, current := range claudeSessions {
		result := claude.UnshareWith(current.jar, current.shares)
		removed += len(result.Removed)
		unshareFailed += len(result.Failed)
	}
	if removed > 0 {
		outcome.acted = true
		fmt.Println(c(fmt.Sprintf("  ✓ removed %d public Claude share link(s)", removed), grn))
	}
	if unshareFailed > 0 {
		outcome.failed = true
		fmt.Println(c(fmt.Sprintf("  ✗ failed to remove %d Claude share link(s)", unshareFailed), red))
	}
	return outcome
}

func confirm(prompt string) bool {
	fmt.Printf("%s [y/N] ", prompt)
	answer, _ := bufio.NewReader(os.Stdin).ReadString('\n')
	answer = strings.TrimSpace(strings.ToLower(answer))
	if answer == "y" || answer == "yes" {
		return true
	}
	fmt.Println("Cancelled.")
	return false
}

func turnOffOpenAI(retryCommand string) offOutcome {
	outcome := offOutcome{}
	fdaBlocked := false
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
		outcome.acted = true
		fmt.Println("  " + c("[OpenAI Account]", bold))
		field("Account", r.Email, "", "")
		field("Discovered via", b.Name, "", "")
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
		fmt.Println(c("    Opening the setting; add your terminal, then re-run `"+retryCommand+"`.", dim))
		openFullDiskAccessSettings()
		outcome.indeterminate = true
	}
	return outcome
}

func mergeOutcomes(values ...offOutcome) offOutcome {
	var merged offOutcome
	for _, value := range values {
		merged.acted = merged.acted || value.acted
		merged.failed = merged.failed || value.failed
		merged.indeterminate = merged.indeterminate || value.indeterminate
		merged.cancelled = merged.cancelled || value.cancelled
	}
	return merged
}

func finishOff(outcome offOutcome, successMessage string) int {
	if outcome.failed {
		return 1
	}
	if outcome.indeterminate {
		if outcome.acted {
			fmt.Println(c("  ! some account settings could not be verified", yel))
		} else {
			fmt.Println(c("  no supported account could be updated", yel))
		}
		return 2
	}
	if !outcome.acted {
		fmt.Println(c("  no supported account could be updated", yel))
		return 2
	}
	fmt.Println(c("  ✓ "+successMessage, grn))
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
	fmt.Println(c("nosnitch", bold), c("· AI account privacy check", dim))
	fmt.Println()

	for _, a := range rep.Accounts {
		label := "OpenAI Account"
		if a.Provider == "anthropic" {
			label = "Claude Account"
		}
		fmt.Println("  " + c("["+label+"]", bold))
		field("Account", a.Email, "", "")
		if a.Plan != "" {
			plan := capitalize(a.Plan)
			if a.Provider == "openai" {
				plan = "ChatGPT " + plan
			}
			field("Plan", plan, "", "")
		}
		field("Discovered via", strings.Join(a.Sources, ", "), "", "")
		if a.Provider == "openai" {
			flagRow("API data sharing", a.APIDataSharing, "API traffic used for training")
			for _, f := range chatgpt.TrainingFeatures {
				if f.Key == chatgpt.CodexTrainingFeatureKey && a.CodexTrainingUnknown() {
					field(f.Label, "UNKNOWN", yel, "ChatGPT account setting could not be read")
					continue
				}
				flagRow(f.Label, a.Training[f.Key], f.OnNote)
			}
		} else {
			if a.ModelImprovement == nil {
				field("Model improvement", "UNKNOWN", yel, "account setting could not be read")
			} else {
				flagRow("Model improvement", a.ModelImprovement, "chats and coding sessions used for training")
			}
			if !a.SharedChatsChecked {
				field("Shared chats", "UNKNOWN", yel, "no readable Claude browser session")
			} else {
				count := len(a.SharedConversations)
				color := grn
				if count > 0 {
					color = red
				}
				field("Shared chats", fmt.Sprintf("%d", count), color, sharedNote(count))
			}
			for _, shared := range a.SharedConversations {
				fmt.Print("  ")
				printSharedChat(shared)
			}
		}
		fmt.Println()
	}

	for _, b := range rep.Blocked {
		fmt.Println(c("  ! "+b.Source+" — "+b.Reason+" to read its session.", yel))
		fmt.Println(c(fullDiskAccessHint(), dim))
		fmt.Println()
	}

	switch {
	case rep.Risk():
		fmt.Println(c("  ✗ privacy exposure found", red), c("— review the account settings above", dim))
	case rep.Indeterminate():
		fmt.Println(c("  ? incomplete", yel), c("— one or more account checks could not be completed", dim))
	default:
		fmt.Println(c("  ✓ no training or public-sharing exposure found", grn))
	}
}

func sharedNote(count int) string {
	if count > 0 {
		return "publicly accessible links found"
	}
	return ""
}

func printSharedChat(shared claude.SharedConversation) {
	name := shared.Name
	if name == "" {
		name = "(untitled)"
	}
	fmt.Printf("    %s  %s\n", c(name, yel), c(shared.URL, dim))
}
