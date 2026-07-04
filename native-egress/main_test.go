package main

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestHealth(t *testing.T) {
	srv := httptest.NewServer(newServer())
	defer srv.Close()
	resp, err := http.Get(srv.URL + "/health")
	if err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode != 200 {
		t.Fatalf("want 200, got %d", resp.StatusCode)
	}
}

func TestResolveAddr(t *testing.T) {
	cases := []struct {
		port    int
		env     string
		want    string
	}{
		{0, "", "127.0.0.1:0"},
		{9876, "", "127.0.0.1:9876"},
		{9876, "0.0.0.0:1234", "0.0.0.0:1234"},
		{0, "127.0.0.1:5555", "127.0.0.1:5555"},
	}
	for _, c := range cases {
		got := resolveAddr(c.port, c.env)
		if got != c.want {
			t.Errorf("resolveAddr(%d, %q) = %q, want %q", c.port, c.env, got, c.want)
		}
	}
}
