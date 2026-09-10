// nosnitch checks and clears AI account training and public-sharing exposure.
//
//	nosnitch                         show account privacy status
//	nosnitch off                     clear all detected exposure
//	nosnitch <provider> training     turn off one provider's training
//	nosnitch claude unshare          remove only public Claude links
package main

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"runtime"
	"strings"
	"time"

	"github.com/circlesac/nosnitch-cli/internal/account"
	"github.com/circlesac/nosnitch-cli/internal/chatgpt"
	"github.com/circlesac/nosnitch-cli/internal/claude"
	"github.com/circlesac/nosnitch-cli/internal/cookies"
	githubprivacy "github.com/circlesac/nosnitch-cli/internal/github"
	"github.com/circlesac/nosnitch-cli/internal/oauth"
	"github.com/circlesac/nosnitch-cli/internal/registry"
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
	case "github":
		os.Exit(runProviderCommand("github"))
	case "account":
		os.Exit(runAccountCommand())
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

func runAccountCommand() int {
	if len(os.Args) < 3 {
		fmt.Fprintln(os.Stderr, "missing account command")
		return 2
	}
	switch os.Args[2] {
	case "list":
		all, e := registry.Load()
		if e != nil {
			fmt.Fprintln(os.Stderr, e)
			return 2
		}
		for _, a := range all {
			fmt.Printf("%s\t%s\t%s\n", a.ID, a.Provider, a.Email)
		}
		return 0
	case "add":
		if len(os.Args) < 4 {
			fmt.Fprintln(os.Stderr, "missing provider")
			return 2
		}
		provider := os.Args[3]
		provider = normalizeProvider(provider)
		if provider == "openai" || provider == "anthropic" {
			token, identity, err := (oauth.Client{}).Login(context.Background(), provider)
			if err != nil {
				fmt.Fprintln(os.Stderr, err)
				return 2
			}
			subject := identity.Subject
			if subject == "" {
				subject = identity.Email
			}
			id := provider + ":" + subject
			credential := registry.Credential{Kind: "oauth", AccessToken: token.AccessToken, RefreshToken: token.RefreshToken, IDToken: token.IDToken, ExpiresAt: token.ExpiresAt}
			if browserCandidates, browserErr := discoverBrowserAccount(provider); browserErr == nil && len(browserCandidates) == 1 &&
				strings.EqualFold(browserCandidates[0].account.Email, identity.Email) {
				credential.Cookies = browserCandidates[0].credential.Cookies
			}
			if err := registry.Register(registry.Account{ID: id, Provider: provider, Email: identity.Email, Subject: identity.Subject, Organization: identity.Organization, AuthKind: "oauth", UpdatedAt: time.Now().UTC()}, credential); err != nil {
				fmt.Fprintln(os.Stderr, err)
				return 2
			}
			fmt.Printf("registered %s (%s)\n", id, identity.Email)
			return 0
		}
		a, credential, err := waitForBrowserAccount(provider)
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			return 2
		}
		if err := registry.Register(a, credential); err != nil {
			fmt.Fprintln(os.Stderr, err)
			return 2
		}
		fmt.Printf("registered %s (%s)\n", a.ID, a.Email)
		return 0
	case "remove":
		if len(os.Args) < 4 {
			fmt.Fprintln(os.Stderr, "missing account id")
			return 2
		}
		if e := registry.Remove(os.Args[3]); e != nil {
			fmt.Fprintln(os.Stderr, e)
			return 2
		}
		return 0
	default:
		fmt.Fprintln(os.Stderr, "unknown account command:", os.Args[2])
		return 2
	}
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

  nosnitch github training [--yes]
      Turn off GitHub Copilot model training.

  nosnitch version

  nosnitch account add <provider>       authenticate and register an account
  nosnitch account list                 list registered accounts
  nosnitch account remove <account-id>  remove an account and cached credential
  nosnitch check --account <account-id>  check one account; reauthenticate when needed
  nosnitch check --all                  check every registered account; reauthenticate when needed

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
	} else if provider == "github" {
		label = "GitHub"
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
		if provider == "github" {
			return runGitHubTrainingOff(hasFlag("--yes"))
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
	if provider == "github" {
		fmt.Fprint(out, `Usage:
  nosnitch github training [--yes]
      Turn off GitHub Copilot model training.
`)
		return
	}
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
	accountID := flagValue("--account")
	if hasFlag("--account") && accountID == "" {
		fmt.Fprintln(os.Stderr, "missing account id after --account")
		return 2
	}
	if accountID != "" || hasFlag("--all") {
		return runRegisteredStatus(asJSON, accountID, !asJSON)
	}
	rep := account.Gather()
	if asJSON {
		out, _ := json.MarshalIndent(rep, "", "  ")
		fmt.Println(string(out))
		return statusCode(rep)
	}
	printStatus(rep)
	return statusCode(rep)
}

var errBrowserSessionMissing = errors.New("no authenticated browser session found")
var errRegisteredCredentialMissing = errors.New("registered credential missing")

func normalizeProvider(value string) string {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "claude":
		return "anthropic"
	case "chatgpt":
		return "openai"
	default:
		return strings.ToLower(strings.TrimSpace(value))
	}
}

type browserAccountCandidate struct {
	account    registry.Account
	credential registry.Credential
}

func discoverBrowserAccount(provider string) ([]browserAccountCandidate, error) {
	seen := map[string]bool{}
	var candidates []browserAccountCandidate
	var blocked []string
	for _, browser := range cookies.Installed() {
		var (
			jar  map[string]string
			err  error
			id   string
			mail string
			org  string
		)
		switch provider {
		case "openai":
			jar, err = browser.ChatGPT()
			if err == nil && jar != nil {
				result := chatgpt.CheckWith(jar)
				if result.OK {
					mail, id = result.Email, result.Email
				}
			}
		case "anthropic":
			jar, err = browser.Claude()
			if err == nil && jar != nil {
				result := claude.CheckWeb(jar)
				if result.OK {
					mail, id = result.Email, result.Email
				}
			}
		case "github":
			jar, err = browser.GitHub()
			if err == nil && jar != nil {
				result := githubprivacy.CheckWith(jar)
				if result.OK {
					id = result.Login
				}
			}
		default:
			return nil, fmt.Errorf("unsupported provider: %s", provider)
		}
		if errors.Is(err, cookies.ErrNeedFullDiskAccess) {
			blocked = append(blocked, browser.Name)
			continue
		}
		if err != nil || jar == nil || id == "" || seen[id] {
			continue
		}
		seen[id] = true
		candidates = append(candidates, browserAccountCandidate{
			account: registry.Account{
				ID:           provider + ":" + id,
				Provider:     provider,
				Email:        mail,
				Organization: org,
				UpdatedAt:    time.Now().UTC(),
				AuthKind:     "browser",
			},
			credential: registry.Credential{Kind: "cookie", Cookies: jar},
		})
	}
	if len(candidates) == 0 && len(blocked) > 0 {
		return nil, fmt.Errorf("%s browser session is unreadable; grant Full Disk Access and retry", strings.Join(blocked, ", "))
	}
	return candidates, nil
}

func waitForBrowserAccount(provider string) (registry.Account, registry.Credential, error) {
	candidates, err := discoverBrowserAccount(provider)
	if err != nil {
		return registry.Account{}, registry.Credential{}, err
	}
	if len(candidates) == 1 {
		return candidates[0].account, candidates[0].credential, nil
	}
	if len(candidates) > 1 {
		ids := make([]string, 0, len(candidates))
		for _, candidate := range candidates {
			ids = append(ids, candidate.account.ID)
		}
		return registry.Account{}, registry.Credential{}, fmt.Errorf("multiple authenticated %s sessions found (%s); use one browser profile at a time", provider, strings.Join(ids, ", "))
	}

	loginURL := map[string]string{
		"anthropic": "https://claude.ai/",
		"github":    "https://github.com/settings/copilot",
		"openai":    "https://chatgpt.com/",
	}[provider]
	if loginURL == "" {
		return registry.Account{}, registry.Credential{}, fmt.Errorf("unsupported provider: %s", provider)
	}
	if err := openURL(loginURL); err != nil {
		return registry.Account{}, registry.Credential{}, fmt.Errorf("open %s login page: %w", provider, err)
	}
	fmt.Fprintf(os.Stderr, "sign in to %s in the browser; waiting for the session\n", provider)
	deadline := time.Now().Add(5 * time.Minute)
	ticker := time.NewTicker(2 * time.Second)
	defer ticker.Stop()
	for {
		candidates, err = discoverBrowserAccount(provider)
		if err != nil {
			return registry.Account{}, registry.Credential{}, err
		}
		if len(candidates) == 1 {
			return candidates[0].account, candidates[0].credential, nil
		}
		if len(candidates) > 1 {
			return registry.Account{}, registry.Credential{}, fmt.Errorf("multiple authenticated %s sessions found (%s); use one browser profile at a time", provider, candidateIDs(candidates))
		}
		if time.Now().After(deadline) {
			return registry.Account{}, registry.Credential{}, fmt.Errorf("%w for %s before timeout", errBrowserSessionMissing, provider)
		}
		<-ticker.C
	}
}

func candidateIDs(candidates []browserAccountCandidate) string {
	ids := make([]string, 0, len(candidates))
	for _, candidate := range candidates {
		ids = append(ids, candidate.account.ID)
	}
	return strings.Join(ids, ", ")
}

func openURL(target string) error {
	var command *exec.Cmd
	switch {
	case strings.TrimSpace(target) == "":
		return errors.New("login URL is empty")
	case runtime.GOOS == "darwin":
		command = exec.Command("open", target)
	case runtime.GOOS == "windows":
		command = exec.Command("rundll32", "url.dll,FileProtocolHandler", target)
	default:
		command = exec.Command("xdg-open", target)
	}
	if err := command.Start(); err != nil {
		return err
	}
	return command.Process.Release()
}

func flagValue(name string) string {
	for i, a := range os.Args {
		if a == name && i+1 < len(os.Args) {
			return os.Args[i+1]
		}
	}
	return ""
}

func runRegisteredStatus(asJSON bool, selector string, allowInteractive bool) int {
	registered, err := registry.Load()
	if err != nil {
		rep := account.Report{Skipped: []string{"account registry: " + err.Error()}}
		if asJSON {
			out, _ := json.MarshalIndent(rep, "", "  ")
			fmt.Println(string(out))
		} else {
			printStatus(rep)
		}
		return statusCode(rep)
	}
	if len(registered) == 0 {
		rep := account.Report{Skipped: []string{"no registered accounts; run `nosnitch account add <provider>`"}}
		if asJSON {
			out, _ := json.MarshalIndent(rep, "", "  ")
			fmt.Println(string(out))
		} else {
			printStatus(rep)
		}
		return statusCode(rep)
	}

	var selected []registry.Account
	for _, registeredAccount := range registered {
		if selector == "" || registeredAccount.ID == selector || registeredAccount.Email == selector {
			selected = append(selected, registeredAccount)
		}
	}
	if selector != "" && len(selected) == 0 {
		rep := account.Report{Skipped: []string{"registered account not found: " + selector}}
		if asJSON {
			out, _ := json.MarshalIndent(rep, "", "  ")
			fmt.Println(string(out))
		} else {
			printStatus(rep)
		}
		return statusCode(rep)
	}

	rep := account.Report{}
	for _, registeredAccount := range selected {
		result, checkErr := checkRegisteredAccount(registeredAccount)
		if allowInteractive && checkErr != nil && shouldReauthenticate(registeredAccount, checkErr) {
			result, checkErr = reauthenticateAndCheck(registeredAccount)
		}
		if result != nil {
			rep.Accounts = append(rep.Accounts, result)
		}
		status := "clean"
		if checkErr != nil {
			status = "error: " + checkErr.Error()
			rep.Skipped = append(rep.Skipped, registeredAccount.ID+": "+checkErr.Error())
		} else if result.Risk() {
			status = "exposure"
		}
		if err := registry.UpdateStatus(registeredAccount.ID, status, time.Now().UTC()); err != nil {
			rep.Skipped = append(rep.Skipped, registeredAccount.ID+": could not update status")
		}
	}
	if asJSON {
		out, _ := json.MarshalIndent(rep, "", "  ")
		fmt.Println(string(out))
		return statusCode(rep)
	}
	printStatus(rep)
	return statusCode(rep)
}

func checkRegisteredAccount(registered registry.Account) (*account.Account, error) {
	result := &account.Account{
		Provider: registered.Provider,
		Email:    registered.Email,
		Sources:  []string{"registered credential"},
	}
	if registered.Provider == "github" {
		result.Login = registered.Email
		result.Email = ""
	}
	credential, err := registry.LoadCredential(registered.ID)
	if err != nil {
		return result, fmt.Errorf("%w: authentication required (credential cache missing)", errRegisteredCredentialMissing)
	}

	if credential.Kind == "oauth" {
		credential, err = refreshRegisteredCredential(registered, credential)
		if err != nil {
			return result, err
		}
		switch registered.Provider {
		case "anthropic":
			identity, identifyErr := (oauth.Client{}).Identify(context.Background(), registered.Provider, oauth.Token{
				AccessToken: credential.AccessToken,
				IDToken:     credential.IDToken,
			})
			if identifyErr != nil {
				return result, identifyErr
			}
			if !sameIdentity(registered, identity.Email, identity.Subject) {
				return result, errors.New("credential belongs to a different account")
			}
			checked := claude.CheckOAuthToken(credential.AccessToken)
			result.ModelImprovement = checked.ModelImprovement
			if len(credential.Cookies) > 0 {
				webChecked := claude.CheckWeb(credential.Cookies)
				if webChecked.OK {
					result.Email = webChecked.Email
					result.SharedConversations = webChecked.SharedConversations
					result.SharedChatsChecked = true
				}
			}
			if !checked.OK {
				return result, errors.New(checked.Reason)
			}
			return result, nil
		case "openai":
			checked := chatgpt.Result{}
			if len(credential.Cookies) > 0 {
				checked = chatgpt.CheckWith(credential.Cookies)
			}
			if !checked.OK {
				checked = chatgpt.CheckWithAccessToken(credential.AccessToken)
			}
			result.Training = checked.Training
			if !checked.OK {
				return result, errors.New(checked.Reason)
			}
			return result, nil
		default:
			return result, errors.New("OAuth checks are unsupported for " + registered.Provider)
		}
	}

	switch registered.Provider {
	case "anthropic":
		checked := claude.CheckWeb(credential.Cookies)
		result.Email = checked.Email
		result.ModelImprovement = checked.ModelImprovement
		result.SharedConversations = checked.SharedConversations
		result.SharedChatsChecked = checked.OK
		if !checked.OK {
			return result, errors.New(checked.Reason)
		}
	case "openai":
		checked := chatgpt.CheckWith(credential.Cookies)
		result.Email = checked.Email
		result.Training = checked.Training
		if !checked.OK {
			return result, errors.New(checked.Reason)
		}
	case "github":
		checked := githubprivacy.CheckWith(credential.Cookies)
		result.Login = checked.Login
		result.Plan = checked.License
		result.GitHubCopilot = &checked.Settings
		if !checked.OK {
			return result, errors.New(checked.Reason)
		}
	default:
		return result, errors.New("unsupported provider: " + registered.Provider)
	}
	if registered.Email != "" && result.Email != "" && !strings.EqualFold(registered.Email, result.Email) {
		return result, errors.New("credential belongs to a different account")
	}
	if registered.Email != "" && result.Login != "" && !strings.EqualFold(registered.Email, result.Login) {
		return result, errors.New("credential belongs to a different account")
	}
	return result, nil
}

func shouldReauthenticate(registered registry.Account, err error) bool {
	if err == nil || (registered.AuthKind != "oauth" && registered.AuthKind != "browser") {
		return false
	}
	if errors.Is(err, errRegisteredCredentialMissing) || errors.Is(err, oauth.ErrReauth) {
		return true
	}
	message := strings.ToLower(err.Error())
	for _, marker := range []string{
		"authentication required",
		"credential was rejected",
		"access token is missing",
		"oauth credential was rejected",
		"session expired",
		"expired session",
		"cloudflare block",
		"signed-in github account not found",
		"http 401",
	} {
		if strings.Contains(message, marker) {
			return true
		}
	}
	return false
}

func reauthenticateAndCheck(registered registry.Account) (*account.Account, error) {
	identity := registered.Email
	if identity == "" {
		identity = registered.ID
	}
	fmt.Fprintf(os.Stderr, "authentication required for %s; sign in in the browser\n", identity)

	switch registered.AuthKind {
	case "oauth":
		token, providerIdentity, err := (oauth.Client{}).Login(context.Background(), registered.Provider)
		if err != nil {
			return &account.Account{
				Provider: registered.Provider,
				Email:    registered.Email,
				Login:    registered.Email,
				Sources:  []string{"registered credential"},
			}, err
		}
		if !sameIdentity(registered, providerIdentity.Email, providerIdentity.Subject) {
			return &account.Account{
				Provider: registered.Provider,
				Email:    registered.Email,
				Login:    registered.Email,
				Sources:  []string{"registered credential"},
			}, errors.New("signed-in account does not match the registered account")
		}
		credential := registry.Credential{
			Kind:         "oauth",
			AccessToken:  token.AccessToken,
			RefreshToken: token.RefreshToken,
			IDToken:      token.IDToken,
			ExpiresAt:    token.ExpiresAt,
		}
		if browserCandidates, browserErr := discoverBrowserAccount(registered.Provider); browserErr == nil {
			for _, candidate := range browserCandidates {
				if strings.EqualFold(candidate.account.Email, providerIdentity.Email) {
					credential.Cookies = candidate.credential.Cookies
					break
				}
			}
		}
		if err := registry.Register(registered, credential); err != nil {
			return &account.Account{
				Provider: registered.Provider,
				Email:    registered.Email,
				Sources:  []string{"registered credential"},
			}, err
		}
	case "browser":
		_, credential, err := waitForRegisteredBrowserAccount(registered)
		if err != nil {
			return &account.Account{
				Provider: registered.Provider,
				Email:    registered.Email,
				Login:    registered.Email,
				Sources:  []string{"registered credential"},
			}, err
		}
		if err := registry.Register(registered, credential); err != nil {
			return &account.Account{
				Provider: registered.Provider,
				Email:    registered.Email,
				Login:    registered.Email,
				Sources:  []string{"registered credential"},
			}, err
		}
	default:
		return &account.Account{
			Provider: registered.Provider,
			Email:    registered.Email,
			Login:    registered.Email,
			Sources:  []string{"registered credential"},
		}, errors.New("account authentication cannot be renewed automatically")
	}

	return checkRegisteredAccount(registered)
}

func waitForRegisteredBrowserAccount(registered registry.Account) (registry.Account, registry.Credential, error) {
	match := func(candidates []browserAccountCandidate) (browserAccountCandidate, bool) {
		for _, candidate := range candidates {
			if candidate.account.ID == registered.ID ||
				(registered.Email != "" && strings.EqualFold(candidate.account.Email, registered.Email)) {
				return candidate, true
			}
		}
		return browserAccountCandidate{}, false
	}

	candidates, err := discoverBrowserAccount(registered.Provider)
	if err != nil {
		return registry.Account{}, registry.Credential{}, err
	}
	if candidate, ok := match(candidates); ok {
		return candidate.account, candidate.credential, nil
	}

	loginURL := map[string]string{
		"anthropic": "https://claude.ai/",
		"github":    "https://github.com/settings/copilot",
		"openai":    "https://chatgpt.com/",
	}[registered.Provider]
	if loginURL == "" {
		return registry.Account{}, registry.Credential{}, fmt.Errorf("unsupported provider: %s", registered.Provider)
	}
	if err := openURL(loginURL); err != nil {
		return registry.Account{}, registry.Credential{}, fmt.Errorf("open %s login page: %w", registered.Provider, err)
	}
	fmt.Fprintf(os.Stderr, "sign in to %s in the browser; waiting for the registered account\n", registered.Provider)

	deadline := time.Now().Add(5 * time.Minute)
	ticker := time.NewTicker(2 * time.Second)
	defer ticker.Stop()
	for {
		candidates, err = discoverBrowserAccount(registered.Provider)
		if err != nil {
			return registry.Account{}, registry.Credential{}, err
		}
		if candidate, ok := match(candidates); ok {
			return candidate.account, candidate.credential, nil
		}
		if time.Now().After(deadline) {
			return registry.Account{}, registry.Credential{},
				fmt.Errorf("%w for %s before timeout", errBrowserSessionMissing, registered.Email)
		}
		<-ticker.C
	}
}

func refreshRegisteredCredential(registered registry.Account, credential registry.Credential) (registry.Credential, error) {
	now := time.Now().UTC()
	token := oauth.Token{
		AccessToken:  credential.AccessToken,
		RefreshToken: credential.RefreshToken,
		IDToken:      credential.IDToken,
		ExpiresAt:    credential.ExpiresAt,
	}
	needsRefresh := token.AccessToken == "" ||
		(!token.ExpiresAt.IsZero() && !token.ExpiresAt.After(now.Add(30*time.Second)))
	if !needsRefresh {
		return credential, nil
	}
	if token.RefreshToken == "" {
		return credential, oauth.ErrReauth
	}
	refreshed, err := (oauth.Client{}).Refresh(context.Background(), registered.Provider, token)
	if err != nil {
		return credential, err
	}
	credential.AccessToken = refreshed.AccessToken
	credential.RefreshToken = refreshed.RefreshToken
	credential.IDToken = refreshed.IDToken
	credential.ExpiresAt = refreshed.ExpiresAt
	if err := registry.SaveCredential(registered.ID, credential); err != nil {
		return credential, err
	}
	return credential, nil
}

func sameIdentity(registered registry.Account, email, subject string) bool {
	if registered.Subject != "" && subject != "" && registered.Subject != subject {
		return false
	}
	return registered.Email == "" || email == "" || strings.EqualFold(registered.Email, email)
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
	registeredClaudeOutcome := turnOffRegisteredOAuth("anthropic")
	registeredOpenAIOutcome := turnOffRegisteredOAuth("openai")
	githubOutcome := turnOffGitHub("nosnitch off")
	return finishOff(mergeOutcomes(claudeOutcome, openAIOutcome, registeredClaudeOutcome,
		registeredOpenAIOutcome, githubOutcome),
		"training and public-sharing exposure turned off")
}

func runOpenAIOff(yes bool) int {
	if !yes && !confirm("Turn off OpenAI Account training settings?") {
		return 0
	}
	fmt.Println(c("nosnitch", bold), c("· turning off OpenAI Account training…", dim))
	fmt.Println()
	return finishOff(mergeOutcomes(
		turnOffOpenAI("nosnitch openai training"),
		turnOffRegisteredOAuth("openai"),
	),
		"OpenAI Account training turned off")
}

func runClaudeTrainingOff(yes bool) int {
	if !yes && !confirm("Turn off Claude Account model improvement?") {
		return 0
	}
	fmt.Println(c("nosnitch", bold), c("· turning off Claude Account training…", dim))
	fmt.Println()
	result := claude.OffCode()
	outcome := offOutcome{}
	if !result.OK {
		if result.Email == "" {
			fmt.Println(c("  no Claude Code account could be updated", yel))
			outcome.indeterminate = true
		} else {
			fmt.Println(c("  ✗ Claude model improvement: "+result.Reason, red))
			outcome.failed = true
		}
	} else {
		fmt.Println("  " + c("[Claude Account]", bold))
		field("Account", result.Email, "", "")
		field("Discovered via", "Claude Code", "", "")
		field("Model improvement", "OFF", grn, "")
		fmt.Println()
		outcome.acted = true
	}
	registered := turnOffRegisteredOAuth("anthropic")
	outcome = mergeOutcomes(outcome, registered)
	return finishOff(outcome, "Claude Account training turned off")
}

func runGitHubTrainingOff(yes bool) int {
	if !yes && !confirm("Turn off GitHub Copilot model training?") {
		return 0
	}
	fmt.Println(c("nosnitch", bold), c("· turning off GitHub Copilot model training…", dim))
	fmt.Println()
	return finishOff(turnOffGitHub("nosnitch github training"),
		"GitHub Copilot model training turned off")
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
	return confirmWith(prompt, os.Stdin, os.Stdout)
}

func confirmWith(prompt string, in io.Reader, out io.Writer) bool {
	fmt.Fprintf(out, "%s [y/N] ", prompt)
	answer, _ := bufio.NewReader(in).ReadString('\n')
	answer = strings.TrimSpace(strings.ToLower(answer))
	if answer == "y" || answer == "yes" {
		return true
	}
	fmt.Fprintln(out, "Cancelled.")
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
			if r.Email != "" {
				outcome.failed = true
				fmt.Println(c("  ! "+r.Email+" (OpenAI): "+r.Reason, yel))
			} else {
				outcome.indeterminate = true
				fmt.Println(c("  ! "+b.Name+" (OpenAI): "+r.Reason, yel))
			}
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

// turnOffRegisteredOAuth updates every explicitly registered OAuth account.
// Browser-backed accounts are handled separately above; accounts whose OAuth
// token can read settings are updated here so `off` covers the full registry.
func turnOffRegisteredOAuth(provider string) offOutcome {
	outcome := offOutcome{}
	registered, err := registry.Load()
	if err != nil {
		fmt.Println(c("  ! registered accounts could not be read: "+err.Error(), yel))
		outcome.indeterminate = true
		return outcome
	}
	for _, account := range registered {
		if account.Provider != provider {
			continue
		}
		credential, err := registry.LoadCredential(account.ID)
		if err != nil || credential.Kind != "oauth" {
			continue
		}
		credential, err = refreshRegisteredCredential(account, credential)
		if err != nil {
			fmt.Println(c("  ! "+account.ID+": "+err.Error(), yel))
			outcome.indeterminate = true
			continue
		}

		switch provider {
		case "anthropic":
			checked := claude.CheckOAuthToken(credential.AccessToken)
			if checked.OK && checked.ModelImprovement != nil && !*checked.ModelImprovement {
				continue
			}
			updated := claude.OffOAuthToken(credential.AccessToken, account.Email)
			if !updated.OK {
				fmt.Println(c("  ✗ "+account.Email+" Claude model improvement: "+updated.Reason, red))
				outcome.failed = true
				continue
			}
			outcome.acted = true
			fmt.Println("  " + c("[Claude Account]", bold))
			field("Account", account.Email, "", "")
			field("Discovered via", "registered credential", "", "")
			field("Model improvement", "OFF", grn, "")
			fmt.Println()
		case "openai":
			checked := chatgpt.CheckWithAccessToken(credential.AccessToken)
			if checked.OK && allTrainingOff(checked.Training) {
				continue
			}
			updated := chatgpt.OffWithAccessToken(credential.AccessToken)
			if !updated.OK {
				fmt.Println(c("  ✗ "+account.Email+" OpenAI training: "+updated.Reason, red))
				outcome.failed = true
				continue
			}
			outcome.acted = true
			fmt.Println("  " + c("[OpenAI Account]", bold))
			field("Account", account.Email, "", "")
			field("Discovered via", "registered credential", "", "")
			for _, feature := range chatgpt.TrainingFeatures {
				state := updated.Results[feature.Key]
				col := grn
				if state != "off" {
					col = red
				}
				field(feature.Label, state, col, "")
			}
			fmt.Println()
		}
	}
	return outcome
}

func allTrainingOff(values map[string]*bool) bool {
	if len(values) != len(chatgpt.TrainingFeatures) {
		return false
	}
	for _, feature := range chatgpt.TrainingFeatures {
		value, ok := values[feature.Key]
		if !ok || value == nil || *value {
			return false
		}
	}
	return true
}

func turnOffGitHub(retryCommand string) offOutcome {
	outcome := offOutcome{}
	fdaBlocked := false
	seen := map[string]bool{}
	for _, browser := range cookies.Installed() {
		jar, err := browser.GitHub()
		if errors.Is(err, cookies.ErrNeedFullDiskAccess) {
			fdaBlocked = true
			continue
		}
		if err != nil {
			outcome.indeterminate = true
			fmt.Println(c("  ! "+browser.Name+" (GitHub): session could not be read", yel))
			continue
		}
		if jar == nil {
			continue
		}
		checked := githubprivacy.CheckWith(jar)
		if !checked.OK {
			outcome.indeterminate = true
			fmt.Println(c("  ! "+browser.Name+" (GitHub): "+checked.Reason, yel))
			continue
		}
		if checked.Reason != "" {
			outcome.indeterminate = true
			fmt.Println(c("  ! "+browser.Name+" (GitHub): "+checked.Reason, yel))
		}
		if seen[checked.Login] {
			continue
		}
		seen[checked.Login] = true
		result := githubprivacy.OffWith(jar)
		if !result.OK {
			outcome.failed = true
			fmt.Println(c("  ✗ GitHub Copilot model training: "+result.Reason, red))
			continue
		}
		outcome.acted = true
		fmt.Println("  " + c("[GitHub Account]", bold))
		field("Account", result.Login, "", "")
		field("Discovered via", browser.Name, "", "")
		field("Model training", "OFF", grn, "")
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
		} else if a.Provider == "github" {
			label = "GitHub Account"
		}
		fmt.Println("  " + c("["+label+"]", bold))
		identity := a.Email
		if a.Provider == "github" {
			identity = a.Login
		}
		field("Account", identity, "", "")
		if a.Plan != "" {
			plan := capitalize(a.Plan)
			if a.Provider == "openai" {
				plan = "ChatGPT " + plan
			}
			planLabel := "Plan"
			if a.Provider == "github" {
				planLabel = "Copilot license"
			}
			field(planLabel, plan, "", "")
		}
		field("Discovered via", strings.Join(a.Sources, ", "), "", "")
		switch a.Provider {
		case "openai":
			flagRow("API data sharing", a.APIDataSharing, "API traffic used for training")
			for _, f := range chatgpt.TrainingFeatures {
				if f.Key == chatgpt.CodexTrainingFeatureKey && a.CodexTrainingUnknown() {
					field(f.Label, "UNKNOWN", yel, "ChatGPT account setting could not be read")
					continue
				}
				flagRow(f.Label, a.Training[f.Key], f.OnNote)
			}
		case "anthropic":
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
		case "github":
			printGitHubCopilot(a.GitHubCopilot)
		}
		fmt.Println()
	}

	for _, b := range rep.Blocked {
		fmt.Println(c("  ! "+b.Source+" — "+b.Reason+" to read its session.", yel))
		fmt.Println(c(fullDiskAccessHint(), dim))
		fmt.Println()
	}
	for _, skipped := range rep.Skipped {
		fmt.Println(c("  ! "+skipped, yel))
	}
	if len(rep.Skipped) > 0 {
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

func printGitHubCopilot(settings *githubprivacy.Settings) {
	if settings == nil {
		field("Model training", "UNKNOWN", yel, "account setting could not be read")
		field("Cloud agent repos", "UNKNOWN", yel, "repository scope could not be read")
		field("Partner agents", "UNKNOWN", yel, "agent settings could not be read")
		return
	}
	if settings.ModelTraining == nil {
		field("Model training", "UNKNOWN", yel, "account setting could not be read")
	} else {
		flagRow("Model training", settings.ModelTraining, "inputs, outputs, and context used for training")
	}
	switch settings.CloudAgentRepositories {
	case "none":
		field("Cloud agent repos", "NONE", grn, "")
	case "selected":
		field("Cloud agent repos", fmt.Sprintf("SELECTED (%d)", settings.SelectedRepositories), yel,
			"review repository selection")
	case "all":
		field("Cloud agent repos", "ALL REPOSITORIES", yel, "review account-wide access")
	default:
		field("Cloud agent repos", "UNKNOWN", yel, "repository scope could not be read")
	}
	if settings.PartnerAgents == nil {
		field("Partner agents", "UNKNOWN", yel, "agent settings could not be read")
		return
	}
	for _, name := range []string{"claude", "codex"} {
		if settings.PartnerAgents[name] == nil {
			field(capitalize(name)+" agent", "UNKNOWN", yel, "agent setting could not be read")
			continue
		}
		flagRow(capitalize(name)+" agent", settings.PartnerAgents[name], "enabled wherever coding agents are allowed")
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
