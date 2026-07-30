package main

import (
	"bytes"
	"strings"
	"testing"

	"github.com/circlesac/nosnitch-cli/internal/account"
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
