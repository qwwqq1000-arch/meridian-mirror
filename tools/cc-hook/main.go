package main

import (
	"bufio"
	"bytes"
	"compress/gzip"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/json"
	"encoding/pem"
	"flag"
	"fmt"
	"io"
	"log"
	"math/big"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

// cc-hook: MitM proxy for claude.exe
//
// Intercepts HTTPS traffic from claude.exe to api.anthropic.com,
// captures full request/response in cleartext. No body cloaking needed —
// claude.exe builds perfect requests, this tool just reads them.
//
// Usage:
//   1. cc-hook --init          # generate CA cert (once)
//   2. cc-hook --port 8888     # start proxy
//   3. Run claude.exe with:
//        HTTPS_PROXY=http://127.0.0.1:8888
//        NODE_EXTRA_CA_CERTS=/path/to/cc-hook-ca.pem
//        claude ...

var (
	flagPort    = flag.Int("port", 8888, "proxy listen port")
	flagInit    = flag.Bool("init", false, "generate CA cert and exit")
	flagCertDir = flag.String("cert-dir", "", "directory for CA cert/key (default: ~/.cc-hook)")
	flagLogDir  = flag.String("log-dir", "", "directory for request logs (default: ./cc-hook-logs)")
	flagVerbose = flag.Bool("v", false, "verbose: print full request/response bodies")
	flagInject  = flag.String("inject", "", "path to JSON file: replace outgoing request body with this content")
	flagMode    = flag.String("mode", "capture", "capture|replay|passthrough")
)

var requestCounter atomic.Int64

func main() {
	flag.Parse()

	certDir := *flagCertDir
	if certDir == "" {
		home, _ := os.UserHomeDir()
		certDir = filepath.Join(home, ".cc-hook")
	}
	os.MkdirAll(certDir, 0o700)

	if *flagInit {
		if err := generateCA(certDir); err != nil {
			log.Fatalf("generate CA: %v", err)
		}
		fmt.Printf("CA cert: %s/ca.pem\n", certDir)
		fmt.Printf("CA key:  %s/ca-key.pem\n", certDir)
		fmt.Println()
		fmt.Println("To use with claude.exe:")
		fmt.Printf("  export HTTPS_PROXY=http://127.0.0.1:%d\n", *flagPort)
		fmt.Printf("  export NODE_EXTRA_CA_CERTS=%s/ca.pem\n", certDir)
		fmt.Println("  claude <args>")
		return
	}

	caCert, caKey, err := loadCA(certDir)
	if err != nil {
		log.Fatalf("load CA (run --init first): %v", err)
	}

	logDir := *flagLogDir
	if logDir == "" {
		logDir = "cc-hook-logs"
	}
	os.MkdirAll(logDir, 0o755)

	proxy := &Proxy{
		caCert: caCert,
		caKey:  caKey,
		logDir: logDir,
		certCache: sync.Map{},
	}

	addr := fmt.Sprintf("127.0.0.1:%d", *flagPort)
	log.Printf("[cc-hook] listening on %s  mode=%s  logs=%s", addr, *flagMode, logDir)
	log.Printf("[cc-hook] CA cert: %s/ca.pem", certDir)
	log.Fatal(http.ListenAndServe(addr, proxy))
}

// ---- CA cert generation ----

func generateCA(dir string) error {
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return err
	}

	tmpl := &x509.Certificate{
		SerialNumber:          big.NewInt(1),
		Subject:               pkix.Name{CommonName: "cc-hook CA", Organization: []string{"cc-hook"}},
		NotBefore:             time.Now().Add(-time.Hour),
		NotAfter:              time.Now().Add(10 * 365 * 24 * time.Hour),
		KeyUsage:              x509.KeyUsageCertSign | x509.KeyUsageCRLSign,
		BasicConstraintsValid: true,
		IsCA:                  true,
		MaxPathLen:            1,
	}

	certDER, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		return err
	}

	certPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: certDER})
	if err := os.WriteFile(filepath.Join(dir, "ca.pem"), certPEM, 0o644); err != nil {
		return err
	}

	keyDER, err := x509.MarshalECPrivateKey(key)
	if err != nil {
		return err
	}
	keyPEM := pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: keyDER})
	return os.WriteFile(filepath.Join(dir, "ca-key.pem"), keyPEM, 0o600)
}

func loadCA(dir string) (*x509.Certificate, *ecdsa.PrivateKey, error) {
	certPEM, err := os.ReadFile(filepath.Join(dir, "ca.pem"))
	if err != nil {
		return nil, nil, err
	}
	block, _ := pem.Decode(certPEM)
	cert, err := x509.ParseCertificate(block.Bytes)
	if err != nil {
		return nil, nil, err
	}

	keyPEM, err := os.ReadFile(filepath.Join(dir, "ca-key.pem"))
	if err != nil {
		return nil, nil, err
	}
	kblock, _ := pem.Decode(keyPEM)
	key, err := x509.ParseECPrivateKey(kblock.Bytes)
	if err != nil {
		return nil, nil, err
	}
	return cert, key, nil
}

// ---- Proxy ----

type Proxy struct {
	caCert    *x509.Certificate
	caKey     *ecdsa.PrivateKey
	logDir    string
	certCache sync.Map // host -> *tls.Certificate
}

func (p *Proxy) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodConnect {
		p.handleConnect(w, r)
		return
	}
	http.Error(w, "only CONNECT supported", http.StatusMethodNotAllowed)
}

func (p *Proxy) handleConnect(w http.ResponseWriter, r *http.Request) {
	host := r.Host
	if !strings.Contains(host, ":") {
		host += ":443"
	}

	hijacker, ok := w.(http.Hijacker)
	if !ok {
		http.Error(w, "hijack not supported", 500)
		return
	}

	// Accept the CONNECT
	w.WriteHeader(200)
	clientConn, _, err := hijacker.Hijack()
	if err != nil {
		log.Printf("[cc-hook] hijack error: %v", err)
		return
	}

	hostname := strings.Split(host, ":")[0]

	// Only MitM api.anthropic.com — pass through everything else
	if hostname != "api.anthropic.com" && hostname != "api.claude.ai" {
		p.tunnel(clientConn, host)
		return
	}

	// MitM: create a TLS server cert for the target host
	tlsCert, err := p.getCert(hostname)
	if err != nil {
		log.Printf("[cc-hook] cert error for %s: %v", hostname, err)
		clientConn.Close()
		return
	}

	tlsConn := tls.Server(clientConn, &tls.Config{
		Certificates: []tls.Certificate{*tlsCert},
	})
	if err := tlsConn.Handshake(); err != nil {
		log.Printf("[cc-hook] TLS handshake with client failed: %v", err)
		tlsConn.Close()
		return
	}

	p.handleMitM(tlsConn, host)
}

// tunnel: pass-through for non-target hosts
func (p *Proxy) tunnel(clientConn net.Conn, host string) {
	defer clientConn.Close()
	upstream, err := net.DialTimeout("tcp", host, 10*time.Second)
	if err != nil {
		return
	}
	defer upstream.Close()
	go io.Copy(upstream, clientConn)
	io.Copy(clientConn, upstream)
}

// handleMitM: intercept HTTP requests over the MitM'd TLS connection
func (p *Proxy) handleMitM(clientTLS net.Conn, upstreamHost string) {
	defer clientTLS.Close()
	br := bufio.NewReader(clientTLS)

	for {
		clientTLS.SetReadDeadline(time.Now().Add(5 * time.Minute))
		req, err := http.ReadRequest(br)
		if err != nil {
			return
		}

		p.proxyRequest(clientTLS, req, upstreamHost)
	}
}

func (p *Proxy) proxyRequest(clientTLS net.Conn, req *http.Request, upstreamHost string) {
	id := requestCounter.Add(1)
	ts := time.Now()

	// Read request body
	var reqBody []byte
	if req.Body != nil {
		reqBody, _ = io.ReadAll(io.LimitReader(req.Body, 50*1024*1024))
		req.Body.Close()
	}

	// Log request
	capture := &CapturedRequest{
		ID:        id,
		Timestamp: ts.Format(time.RFC3339),
		Method:    req.Method,
		URL:       fmt.Sprintf("https://%s%s", upstreamHost, req.URL.RequestURI()),
		Headers:   flattenHeaders(req.Header),
		BodySize:  len(reqBody),
	}

	// Extract key fields for display
	model, msgCount := extractRequestMeta(reqBody)

	log.Printf("[cc-hook] #%d → %s %s  model=%s msgs=%d body=%d",
		id, req.Method, req.URL.Path, model, msgCount, len(reqBody))

	if *flagVerbose {
		log.Printf("[cc-hook] #%d headers: %v", id, capture.Headers)
	}

	// Save full request body
	reqLogFile := filepath.Join(p.logDir, fmt.Sprintf("%04d-req.json", id))
	saveRequestLog(reqLogFile, reqBody, capture)

	// Optionally inject replacement body
	sendBody := reqBody
	if *flagInject != "" && req.Method == "POST" && strings.Contains(req.URL.Path, "/messages") {
		if injected, err := os.ReadFile(*flagInject); err == nil {
			sendBody = injected
			log.Printf("[cc-hook] #%d body injected from %s (%d bytes)", id, *flagInject, len(injected))
		}
	}

	// Forward to upstream
	upstreamReq, _ := http.NewRequest(req.Method, capture.URL, bytes.NewReader(sendBody))
	upstreamReq.Header = req.Header.Clone()

	upstreamTLS := &tls.Config{ServerName: strings.Split(upstreamHost, ":")[0]}
	transport := &http.Transport{TLSClientConfig: upstreamTLS}
	resp, err := transport.RoundTrip(upstreamReq)
	if err != nil {
		log.Printf("[cc-hook] #%d upstream error: %v", id, err)
		clientTLS.Write([]byte("HTTP/1.1 502 Bad Gateway\r\n\r\n"))
		return
	}
	defer resp.Body.Close()

	// Read response
	respBody, _ := io.ReadAll(resp.Body)

	// Decompress gzip if needed
	displayBody := respBody
	if resp.Header.Get("Content-Encoding") == "gzip" {
		if gr, err := gzip.NewReader(bytes.NewReader(respBody)); err == nil {
			displayBody, _ = io.ReadAll(gr)
			gr.Close()
		}
	}

	// Extract response meta
	respModel, inputTok, outputTok := extractResponseInfo(displayBody)

	log.Printf("[cc-hook] #%d ← %d  model=%s  input=%d output=%d  body=%d",
		id, resp.StatusCode, respModel, inputTok, outputTok, len(respBody))

	// Save response
	respLogFile := filepath.Join(p.logDir, fmt.Sprintf("%04d-resp.json", id))
	saveResponseLog(respLogFile, displayBody, resp.StatusCode, flattenHeaders(resp.Header))

	// Write response back to client
	var respBuf bytes.Buffer
	fmt.Fprintf(&respBuf, "HTTP/1.1 %d %s\r\n", resp.StatusCode, resp.Status)
	resp.Header.Write(&respBuf)
	respBuf.WriteString("\r\n")
	respBuf.Write(respBody)
	clientTLS.Write(respBuf.Bytes())
}

// ---- TLS cert generation (per-host, cached) ----

func (p *Proxy) getCert(hostname string) (*tls.Certificate, error) {
	if cached, ok := p.certCache.Load(hostname); ok {
		return cached.(*tls.Certificate), nil
	}

	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return nil, err
	}

	tmpl := &x509.Certificate{
		SerialNumber: big.NewInt(time.Now().UnixNano()),
		Subject:      pkix.Name{CommonName: hostname},
		DNSNames:     []string{hostname},
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     time.Now().Add(24 * time.Hour),
		KeyUsage:     x509.KeyUsageDigitalSignature,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
	}

	certDER, err := x509.CreateCertificate(rand.Reader, tmpl, p.caCert, &key.PublicKey, p.caKey)
	if err != nil {
		return nil, err
	}

	tlsCert := &tls.Certificate{
		Certificate: [][]byte{certDER},
		PrivateKey:  key,
	}
	p.certCache.Store(hostname, tlsCert)
	return tlsCert, nil
}

// ---- Request parsing helpers ----

type CapturedRequest struct {
	ID        int64             `json:"id"`
	Timestamp string            `json:"timestamp"`
	Method    string            `json:"method"`
	URL       string            `json:"url"`
	Headers   map[string]string `json:"headers"`
	BodySize  int               `json:"body_size"`
}

func flattenHeaders(h http.Header) map[string]string {
	m := make(map[string]string, len(h))
	for k, v := range h {
		lk := strings.ToLower(k)
		if lk == "authorization" {
			if len(v[0]) > 15 {
				m[k] = v[0][:10] + "***" + v[0][len(v[0])-4:]
			} else {
				m[k] = "***"
			}
		} else {
			m[k] = strings.Join(v, ", ")
		}
	}
	return m
}

func extractRequestMeta(body []byte) (model string, msgCount int) {
	var req struct {
		Model    string `json:"model"`
		Messages []any  `json:"messages"`
	}
	if json.Unmarshal(body, &req) == nil {
		return req.Model, len(req.Messages)
	}
	return "?", 0
}

func extractResponseInfo(body []byte) (model string, input, output int) {
	// Try JSON response
	var resp struct {
		Model string `json:"model"`
		Usage struct {
			InputTokens  int `json:"input_tokens"`
			OutputTokens int `json:"output_tokens"`
		} `json:"usage"`
	}
	if json.Unmarshal(body, &resp) == nil && resp.Model != "" {
		return resp.Model, resp.Usage.InputTokens, resp.Usage.OutputTokens
	}

	// Try SSE: find last usage line
	for _, line := range bytes.Split(body, []byte("\n")) {
		line = bytes.TrimPrefix(line, []byte("data: "))
		if json.Unmarshal(line, &resp) == nil {
			if resp.Model != "" {
				model = resp.Model
			}
			if resp.Usage.InputTokens > 0 {
				input = resp.Usage.InputTokens
			}
			if resp.Usage.OutputTokens > 0 {
				output = resp.Usage.OutputTokens
			}
		}
	}
	return
}

func saveRequestLog(path string, body []byte, meta *CapturedRequest) {
	f, err := os.Create(path)
	if err != nil {
		return
	}
	defer f.Close()

	enc := json.NewEncoder(f)
	enc.SetEscapeHTML(false)
	enc.SetIndent("", "  ")
	enc.Encode(map[string]any{
		"meta": meta,
		"body": json.RawMessage(body),
	})
}

func saveResponseLog(path string, body []byte, status int, headers map[string]string) {
	f, err := os.Create(path)
	if err != nil {
		return
	}
	defer f.Close()

	enc := json.NewEncoder(f)
	enc.SetEscapeHTML(false)
	enc.SetIndent("", "  ")

	// Try to parse body as JSON for pretty output
	var parsed any
	if json.Unmarshal(body, &parsed) == nil {
		enc.Encode(map[string]any{
			"status":  status,
			"headers": headers,
			"body":    parsed,
		})
	} else if bytes.Contains(body, []byte("event:")) || bytes.Contains(body, []byte("data:")) {
		// SSE - save as string
		enc.Encode(map[string]any{
			"status":  status,
			"headers": headers,
			"body_sse": string(body),
		})
	} else {
		enc.Encode(map[string]any{
			"status":  status,
			"headers": headers,
			"body_raw": string(body),
		})
	}
}
