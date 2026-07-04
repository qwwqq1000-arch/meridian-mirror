package main

import (
	"context"
	"net"
	"net/http"

	tls "github.com/refraction-networking/utls"
	"golang.org/x/net/proxy"
)

func dialUpstream(addr string) (net.Conn, error) {
	return proxy.FromEnvironment().Dial("tcp", addr)
}

// claudeCodeClientHello builds a TLS ClientHelloSpec matching the real Claude
// Code CLI (Bun runtime, Node v24.x). Captured via CONNECT proxy 2026-06-24.
// JA3: d871d02cecbde59abbf8f4806134addf. ALPN: http/1.1 only, no GREASE,
// fixed extension order.
func claudeCodeClientHello() *tls.ClientHelloSpec {
	return &tls.ClientHelloSpec{
		TLSVersMin: tls.VersionTLS10,
		TLSVersMax: tls.VersionTLS13,
		CipherSuites: []uint16{
			tls.TLS_AES_128_GCM_SHA256,                  // 0x1301
			tls.TLS_AES_256_GCM_SHA384,                  // 0x1302
			tls.TLS_CHACHA20_POLY1305_SHA256,             // 0x1303
			tls.TLS_ECDHE_ECDSA_WITH_AES_128_GCM_SHA256, // 0xc02b
			tls.TLS_ECDHE_RSA_WITH_AES_128_GCM_SHA256,   // 0xc02f
			tls.TLS_ECDHE_ECDSA_WITH_AES_256_GCM_SHA384, // 0xc02c
			tls.TLS_ECDHE_RSA_WITH_AES_256_GCM_SHA384,   // 0xc030
			tls.TLS_ECDHE_ECDSA_WITH_CHACHA20_POLY1305,  // 0xcca9
			tls.TLS_ECDHE_RSA_WITH_CHACHA20_POLY1305,    // 0xcca8
			tls.TLS_ECDHE_ECDSA_WITH_AES_128_CBC_SHA,    // 0xc009
			tls.TLS_ECDHE_RSA_WITH_AES_128_CBC_SHA,      // 0xc013
			tls.TLS_ECDHE_ECDSA_WITH_AES_256_CBC_SHA,    // 0xc00a
			tls.TLS_ECDHE_RSA_WITH_AES_256_CBC_SHA,      // 0xc014
			tls.TLS_RSA_WITH_AES_128_GCM_SHA256,         // 0x009c
			tls.TLS_RSA_WITH_AES_256_GCM_SHA384,         // 0x009d
			tls.TLS_RSA_WITH_AES_128_CBC_SHA,            // 0x002f
			tls.TLS_RSA_WITH_AES_256_CBC_SHA,            // 0x0035
		},
		Extensions: []tls.TLSExtension{
			&tls.SNIExtension{},                           // 0
			&tls.UtlsExtendedMasterSecretExtension{},      // 23
			&tls.RenegotiationInfoExtension{               // 65281
				Renegotiation: tls.RenegotiateOnceAsClient,
			},
			&tls.SupportedCurvesExtension{Curves: []tls.CurveID{ // 10
				tls.X25519,    // 0x001d
				tls.CurveP256, // 0x0017
				tls.CurveP384, // 0x0018
			}},
			&tls.SupportedPointsExtension{SupportedPoints: []byte{0}}, // 11
			&tls.SessionTicketExtension{},                             // 35
			&tls.ALPNExtension{AlpnProtocols: []string{"http/1.1"}},  // 16
			&tls.StatusRequestExtension{},                             // 5
			&tls.SignatureAlgorithmsExtension{SupportedSignatureAlgorithms: []tls.SignatureScheme{ // 13
				tls.ECDSAWithP256AndSHA256, // 0x0403
				tls.PSSWithSHA256,          // 0x0804
				tls.PKCS1WithSHA256,        // 0x0401
				tls.ECDSAWithP384AndSHA384, // 0x0503
				tls.PSSWithSHA384,          // 0x0805
				tls.PKCS1WithSHA384,        // 0x0501
				tls.PSSWithSHA512,          // 0x0806
				tls.PKCS1WithSHA512,        // 0x0601
				tls.PKCS1WithSHA1,          // 0x0201
			}},
			&tls.GenericExtension{Id: 18}, // token_binding (Node emits empty)
			&tls.KeyShareExtension{KeyShares: []tls.KeyShare{ // 51
				{Group: tls.X25519},
			}},
			&tls.PSKKeyExchangeModesExtension{Modes: []uint8{tls.PskModeDHE}}, // 45
			&tls.SupportedVersionsExtension{Versions: []uint16{ // 43
				tls.VersionTLS13,
				tls.VersionTLS12,
			}},
			&tls.UtlsPaddingExtension{GetPaddingLen: tls.BoringPaddingStyle}, // 21
		},
	}
}

func NewUTLSTransport() http.RoundTripper {
	return &http.Transport{
		ForceAttemptHTTP2: false,
		DialTLSContext: func(ctx context.Context, network, addr string) (net.Conn, error) {
			host, _, _ := net.SplitHostPort(addr)
			conn, err := dialUpstream(addr)
			if err != nil {
				return nil, err
			}
			uconn := tls.UClient(conn, &tls.Config{
				ServerName: host,
			}, tls.HelloCustom)
			if err := uconn.ApplyPreset(claudeCodeClientHello()); err != nil {
				conn.Close()
				return nil, err
			}
			if err := uconn.HandshakeContext(ctx); err != nil {
				conn.Close()
				return nil, err
			}
			return uconn, nil
		},
	}
}
