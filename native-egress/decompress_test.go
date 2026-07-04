package main

import (
	"bytes"
	"compress/gzip"
	"io"
	"net/http"
	"testing"
)

func TestDecodedBodyGzip(t *testing.T) {
	var buf bytes.Buffer
	gw := gzip.NewWriter(&buf)
	gw.Write([]byte("hello-sse"))
	gw.Close()
	resp := &http.Response{
		Header: http.Header{"Content-Encoding": []string{"gzip"}},
		Body:   io.NopCloser(bytes.NewReader(buf.Bytes())),
	}
	out, _ := io.ReadAll(decodedBody(resp))
	if string(out) != "hello-sse" {
		t.Fatalf("gzip not decoded: %q", out)
	}
}

func TestDecodedBodyIdentity(t *testing.T) {
	resp := &http.Response{
		Header: http.Header{},
		Body:   io.NopCloser(bytes.NewReader([]byte("plain"))),
	}
	out, _ := io.ReadAll(decodedBody(resp))
	if string(out) != "plain" {
		t.Fatalf("identity mangled: %q", out)
	}
}
