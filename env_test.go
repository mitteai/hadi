package main

import "testing"

func TestEnvSetReplacesInPlace(t *testing.T) {
	got := envSet("A=1\nB=2\n", []string{"A=9"})
	want := "A=9\nB=2\n"
	if got != want {
		t.Errorf("got %q want %q", got, want)
	}
}

func TestEnvSetAppendsNew(t *testing.T) {
	got := envSet("A=1\n", []string{"NEW=x"})
	if got != "A=1\nNEW=x\n" {
		t.Errorf("got %q", got)
	}
}

func TestEnvSetOnEmpty(t *testing.T) {
	if got := envSet("", []string{"A=1"}); got != "A=1\n" {
		t.Errorf("got %q", got)
	}
}

func TestEnvSetValueWithEquals(t *testing.T) {
	// Secrets contain '='; only the first one splits key from value.
	got := envSet("", []string{"TOKEN=abc=def=="})
	if got != "TOKEN=abc=def==\n" {
		t.Errorf("got %q", got)
	}
}

func TestEnvUnset(t *testing.T) {
	got := envUnset("A=1\nB=2\nC=3\n", []string{"B"})
	if got != "A=1\nC=3\n" {
		t.Errorf("got %q", got)
	}
}

func TestEnvUnsetDoesNotPrefixMatch(t *testing.T) {
	// Unsetting B must not remove B2.
	got := envUnset("B=1\nB2=2\n", []string{"B"})
	if got != "B2=2\n" {
		t.Errorf("got %q", got)
	}
}
