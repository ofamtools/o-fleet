package main

import (
	"bytes"
	"strings"
	"testing"

	"ofam.dev/ochazuke/lexiconcheck"
)

// o-fleet's registered operations are held to the lexicon's naming rules
// (lexicon plan): one word has one part of speech, nouns are singular, a
// namespace is not a noun or action word, a key has at most three parts.
// testdata/lexicon-words.tsv is a copy of ochazuke's word list.
func TestLexicon(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("HOME", dir)
	t.Setenv("XDG_CONFIG_HOME", dir+"/config")
	t.Setenv("XDG_STATE_HOME", dir+"/state")
	t.Setenv("XDG_DATA_HOME", dir+"/data")
	a, err := New()
	if err != nil {
		t.Fatal(err)
	}
	lexiconcheck.Check(t, lexiconcheck.Config{
		Operations: &a.r.Operations,
		MainNouns:  map[string]string{"fleet": "machine"},
		Words:      "testdata/lexicon-words.tsv",
		Baseline:   "testdata/lexiconcheck.txt",
	})
}

// The catalog's state source is the lists verb, and no state verb is left.
func TestListsIsTheStateSource(t *testing.T) {
	isolate(t)
	out := run(t, "machine", "lists")
	if strings.TrimSpace(out) != "" && !strings.Contains(out, `"noun"`) {
		t.Fatalf("machine lists answered %q", out)
	}
	var buf, errb bytes.Buffer
	if err := Main([]string{"machine", "state"}, strings.NewReader(""), &buf, &errb); err == nil {
		t.Fatal("machine state still runs")
	}
}
