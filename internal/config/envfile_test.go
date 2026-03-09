package config

import (
	"strings"
	"testing"
)

func TestParseEnvFallsBackToStrippingMalformedDoubleQuotes(t *testing.T) {
	values, err := ParseEnv(strings.NewReader("KEY=\"foo\\z\"\n"))
	if err != nil {
		t.Fatalf("ParseEnv returned error: %v", err)
	}

	if got, want := values["KEY"], "foo\\z"; got != want {
		t.Fatalf("KEY = %q, want %q", got, want)
	}
}

func TestParseEnvUnquotesValidDoubleQuotedValue(t *testing.T) {
	values, err := ParseEnv(strings.NewReader("KEY=\"foo\\nbar\"\n"))
	if err != nil {
		t.Fatalf("ParseEnv returned error: %v", err)
	}

	if got, want := values["KEY"], "foo\nbar"; got != want {
		t.Fatalf("KEY = %q, want %q", got, want)
	}
}
