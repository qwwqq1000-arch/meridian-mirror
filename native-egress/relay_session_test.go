package main

import "testing"

func TestConversationSessionIDStableAndPerConversation(t *testing.T) {
	a := conversationSessionID("acct", "conv-1")
	b := conversationSessionID("acct", "conv-1")
	c := conversationSessionID("acct", "conv-2")
	if a != b {
		t.Fatal("same conversation must yield same session id")
	}
	if a == c {
		t.Fatal("different conversations must yield different session ids")
	}
	if len(a) != 36 {
		t.Fatalf("not a uuid: %q", a)
	}
}
