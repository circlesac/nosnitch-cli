package main

import (
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
