/**
 * egressProxy.ts — parse, test, and persist a SOCKS5 (or HTTP) egress proxy
 * used for outbound traffic. Leaf module: only depends on node:net + settings.
 *
 * Accepts the convenient `scheme://host:port:user:pass` form (what most proxy
 * vendors hand out) as well as the standard `scheme://user:pass@host:port` URL.
 * The proxy can be tested without any external dependency via a pure-Node
 * SOCKS5 handshake that CONNECTs through the proxy and fetches the exit IP.
 */

import { connect as netConnect, type Socket } from "node:net"

export interface ParsedProxy {
  scheme: "socks5" | "socks5h" | "http" | "https"
  host: string
  port: number
  user?: string
  pass?: string
  /** Normalized standard URL: scheme://[user:pass@]host:port (auth %-encoded). */
  url: string
}

/**
 * Parse a proxy string. Accepts:
 *   - vendor form:   socks5://23.148.60.245:9004:user:pass
 *   - standard URL:  socks5://user:pass@23.148.60.245:9004
 *   - no scheme:     23.148.60.245:9004:user:pass  (assumes socks5)
 * Returns null when it cannot be parsed.
 */
export function parseProxy(raw: string): ParsedProxy | null {
  let s = (raw || "").trim()
  if (!s) return null

  let scheme: ParsedProxy["scheme"] = "socks5"
  const m = s.match(/^(socks5h?|https?):\/\//i)
  if (m) {
    scheme = m[1]!.toLowerCase() as ParsedProxy["scheme"]
    s = s.slice(m[0].length)
  }

  let host = ""
  let port = NaN
  let user: string | undefined
  let pass: string | undefined

  if (s.includes("@")) {
    const at = s.lastIndexOf("@")
    const auth = s.slice(0, at)
    const hp = s.slice(at + 1)
    const ai = auth.indexOf(":")
    if (ai >= 0) {
      user = decodeURIComponent(auth.slice(0, ai))
      pass = decodeURIComponent(auth.slice(ai + 1))
    } else if (auth) {
      user = decodeURIComponent(auth)
    }
    const hpParts = hp.split(":")
    host = hpParts[0] ?? ""
    port = parseInt(hpParts[1] ?? "", 10)
  } else {
    const parts = s.split(":")
    if (parts.length < 2) return null
    host = parts[0] ?? ""
    port = parseInt(parts[1] ?? "", 10)
    if (parts.length >= 4) {
      user = parts[2]
      pass = parts.slice(3).join(":") // allow ':' inside the password
    } else if (parts.length === 3) {
      user = parts[2]
    }
  }

  if (!host || !Number.isFinite(port) || port <= 0 || port > 65535) return null

  const authPart = user
    ? `${encodeURIComponent(user)}${pass !== undefined ? ":" + encodeURIComponent(pass) : ""}@`
    : ""
  const url = `${scheme}://${authPart}${host}:${port}`
  return { scheme, host, port, user, pass, url }
}

export interface ProxyTestResult {
  ok: boolean
  exitIp?: string
  latencyMs?: number
  error?: string
}

const TEST_HOST = "api.ipify.org"
const TEST_PORT = 80

/**
 * Test a SOCKS5 proxy with a pure-Node handshake: greet → (user/pass auth) →
 * CONNECT api.ipify.org:80 → HTTP GET → parse the returned exit IP. Proves the
 * proxy is reachable, the credentials are valid, and it can egress to the
 * internet. HTTP(S) proxies fall back to a TCP reachability check.
 */
export function testProxy(p: ParsedProxy, timeoutMs = 12000): Promise<ProxyTestResult> {
  return new Promise((resolve) => {
    const start = Date.now()
    let done = false
    let stage = 0 // 0 connecting, 1 greeted, 2 authed, 3 connected, 4 http
    let buf = Buffer.alloc(0)
    let sock: Socket

    const finish = (r: ProxyTestResult) => {
      if (done) return
      done = true
      try {
        sock?.destroy()
      } catch {}
      resolve(r)
    }

    if (p.scheme === "http" || p.scheme === "https") {
      // Minimal reachability check for HTTP proxies (full CONNECT test omitted).
      sock = netConnect({ host: p.host, port: p.port })
      sock.setTimeout(timeoutMs)
      sock.on("timeout", () => finish({ ok: false, error: "timeout connecting to proxy" }))
      sock.on("error", (e) => finish({ ok: false, error: String((e as Error).message || e) }))
      sock.on("connect", () => finish({ ok: true, latencyMs: Date.now() - start }))
      return
    }

    sock = netConnect({ host: p.host, port: p.port })
    sock.setTimeout(timeoutMs)
    sock.on("timeout", () => finish({ ok: false, error: "timeout (proxy unreachable or slow)" }))
    sock.on("error", (e) => finish({ ok: false, error: String((e as Error).message || e) }))

    const sendConnect = () => {
      const hostBuf = Buffer.from(TEST_HOST, "ascii")
      const req = Buffer.concat([
        Buffer.from([0x05, 0x01, 0x00, 0x03, hostBuf.length]),
        hostBuf,
        Buffer.from([(TEST_PORT >> 8) & 0xff, TEST_PORT & 0xff]),
      ])
      buf = Buffer.alloc(0)
      stage = 3
      sock.write(req)
    }

    sock.on("connect", () => {
      const greet = p.user ? Buffer.from([0x05, 0x02, 0x00, 0x02]) : Buffer.from([0x05, 0x01, 0x00])
      stage = 1
      sock.write(greet)
    })

    sock.on("data", (d: Buffer) => {
      buf = Buffer.concat([buf, d])

      if (stage === 1) {
        if (buf.length < 2) return
        const method = buf[1]!
        buf = buf.subarray(2)
        if (method === 0x02) {
          const u = Buffer.from(p.user ?? "", "utf8")
          const pw = Buffer.from(p.pass ?? "", "utf8")
          const auth = Buffer.concat([Buffer.from([0x01, u.length]), u, Buffer.from([pw.length]), pw])
          stage = 2
          sock.write(auth)
          return
        }
        if (method === 0x00) return sendConnect()
        if (method === 0xff) return finish({ ok: false, error: "proxy rejected auth methods (needs different credentials)" })
        return finish({ ok: false, error: `proxy chose unsupported auth method 0x${method.toString(16)}` })
      }

      if (stage === 2) {
        if (buf.length < 2) return
        const status = buf[1]
        buf = buf.subarray(2)
        if (status !== 0x00) return finish({ ok: false, error: "auth failed — wrong username/password" })
        return sendConnect()
      }

      if (stage === 3) {
        if (buf.length < 4) return
        if (buf[1] !== 0x00) return finish({ ok: false, error: `CONNECT refused by proxy (code 0x${buf[1]!.toString(16)})` })
        const atyp = buf[3]
        const addrLen = atyp === 0x01 ? 4 : atyp === 0x04 ? 16 : atyp === 0x03 ? 1 + (buf[4] ?? 0) : -1
        if (addrLen < 0) return finish({ ok: false, error: "malformed CONNECT reply" })
        const replyLen = 4 + addrLen + 2
        if (buf.length < replyLen) return
        buf = buf.subarray(replyLen)
        stage = 4
        sock.write(
          `GET /?format=text HTTP/1.1\r\nHost: ${TEST_HOST}\r\nUser-Agent: curl/8\r\nAccept: */*\r\nConnection: close\r\n\r\n`,
        )
        return
      }
    })

    sock.on("close", () => {
      if (done) return
      if (stage === 4) {
        const text = buf.toString("utf8")
        const idx = text.indexOf("\r\n\r\n")
        const body = idx >= 0 ? text.slice(idx + 4).trim() : ""
        const ipMatch = body.match(/\b\d{1,3}(?:\.\d{1,3}){3}\b/) || body.match(/[0-9a-f:]{3,}/i)
        if (ipMatch) return finish({ ok: true, exitIp: ipMatch[0], latencyMs: Date.now() - start })
        return finish({ ok: true, latencyMs: Date.now() - start }) // CONNECT worked even if IP parse failed
      }
      finish({ ok: false, error: "proxy closed connection during handshake" })
    })
  })
}

/**
 * Export (or clear) the proxy as standard env vars so spawned children — the
 * Claude SDK subprocess and the native-egress sidecar, which inherit the
 * meridian process env — route their outbound traffic through it.
 */
export function applyProxyEnv(url: string | undefined): void {
  const allNames = ["ALL_PROXY", "all_proxy"]
  const httpNames = ["HTTPS_PROXY", "https_proxy", "HTTP_PROXY", "http_proxy"]
  if (url && url.trim()) {
    for (const n of allNames) process.env[n] = url
    // For HTTP/HTTPS proxies, also set HTTP(S)_PROXY so Node's undici (token
    // refresh, oauth-usage) routes through the same proxy. Without this, token
    // refreshes use the host IP while API messages use the proxy IP — the IP
    // mismatch is a ban signal.
    // SOCKS5 proxies: undici cannot speak socks5, so we only set ALL_PROXY
    // (which the Go sidecar reads). Node-side fetches bypass the proxy — an
    // accepted limitation until a local socks5-to-http bridge is added.
    const isSocks = /^socks5/i.test(url)
    if (isSocks) {
      for (const n of httpNames) delete process.env[n]
    } else {
      for (const n of httpNames) process.env[n] = url
    }
  } else {
    for (const n of allNames) delete process.env[n]
    for (const n of httpNames) delete process.env[n]
  }
}
