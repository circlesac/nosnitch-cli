package main

import (
	"bytes"
	"errors"
	"strings"
	"testing"

	"github.com/circlesac/nosnitch-cli/internal/account"
	"github.com/circlesac/nosnitch-cli/internal/chatgpt"
	"github.com/circlesac/nosnitch-cli/internal/oauth"
	"github.com/circlesac/nosnitch-cli/internal/registry"
)

func TestStatusCodeIsIncompleteWhenCodexTrainingIsUnknown(t *testing.T) {
	rep := account.Report{
		Accounts: []*account.Account{{
			Provider: "openai",
			Sources:  []string{"Codex CLI"},
		}},
	}

	if got := statusCode(rep); got != 2 {
		t.Fatalf("statusCode() = %d, want 2", got)
	}
}

func TestConfirmationDefaultsToNo(t *testing.T) {
	for _, input := range []string{"\n", "n\n", "no\n"} {
		var output bytes.Buffer
		if confirmWith("Change setting?", strings.NewReader(input), &output) {
			t.Fatalf("confirmWith(%q) = true", input)
		}
		if !strings.Contains(output.String(), "Cancelled.") {
			t.Fatalf("confirmWith(%q) output = %q", input, output.String())
		}
	}
}

func TestConfirmationAcceptsYes(t *testing.T) {
	for _, input := range []string{"y\n", "YES\n"} {
		var output bytes.Buffer
		if !confirmWith("Change setting?", strings.NewReader(input), &output) {
			t.Fatalf("confirmWith(%q) = false", input)
		}
		if strings.Contains(output.String(), "Cancelled.") {
			t.Fatalf("confirmWith(%q) output = %q", input, output.String())
		}
	}
}

func TestAllTrainingOffRequiresEveryFeature(t *testing.T) {
	off := false
	values := map[string]*bool{}
	for _, feature := range chatgpt.TrainingFeatures {
		values[feature.Key] = &off
	}
	if !allTrainingOff(values) {
		t.Fatal("allTrainingOff() = false for every explicit false setting")
	}
	delete(values, chatgpt.CodexTrainingFeatureKey)
	if allTrainingOff(values) {
		t.Fatal("allTrainingOff() = true when a feature is missing")
	}
}

func TestShouldReauthenticateOnlyForAuthFailures(t *testing.T) {
	account := registry.Account{Provider: "anthropic", AuthKind: "oauth"}
	if !shouldReauthenticate(account, oauth.ErrReauth) {
		t.Fatal("shouldReauthenticate() = false for ErrReauth")
	}
	if !shouldReauthenticate(account, errors.New("Claude session expired or blocked (HTTP 401)")) {
		t.Fatal("shouldReauthenticate() = false for expired session")
	}
	if shouldReauthenticate(account, errors.New("request temporarily unavailable")) {
		t.Fatal("shouldReauthenticate() = true for transient failure")
	}
}
