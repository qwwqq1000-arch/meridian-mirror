package main

import (
	"compress/flate"
	"compress/gzip"
	"io"
	"net/http"
	"strings"

	"github.com/andybalholm/brotli"
	"github.com/klauspost/compress/zstd"
)

// decodedBody returns a reader that transparently decodes resp.Body according to
// its Content-Encoding. Real CC advertises "gzip, deflate, br, zstd" and undici
// decodes whatever Anthropic returns (gzip observed). We do the same. The caller
// must NOT forward the content-encoding/content-length headers downstream (the
// stream path strips them; the non-stream path builds its own headers) — that is
// what avoided the historical double-decode (6737efc). Unknown/identity passes
// through unchanged.
func decodedBody(resp *http.Response) io.Reader {
	enc := strings.ToLower(strings.TrimSpace(resp.Header.Get("Content-Encoding")))
	switch enc {
	case "", "identity":
		return resp.Body
	case "gzip":
		if r, err := gzip.NewReader(resp.Body); err == nil {
			return r
		}
	case "deflate":
		return flate.NewReader(resp.Body)
	case "br":
		return brotli.NewReader(resp.Body)
	case "zstd":
		if r, err := zstd.NewReader(resp.Body); err == nil {
			return r.IOReadCloser()
		}
	}
	return resp.Body
}
