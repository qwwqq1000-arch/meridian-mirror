package main

import (
	"net/http"
	"testing"
)

func TestNewUTLSTransportImplementsRoundTripper(t *testing.T) {
	var _ http.RoundTripper = NewUTLSTransport()
}
