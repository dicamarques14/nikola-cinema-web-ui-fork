package urlsource

import (
	"net/http"
	"net/textproto"
	"strings"
)

// forwardRequestHeaders are the only headers of the viewer's (or the
// seeder's) request passed on to the origin. Cookies, the session, the
// viewer's address -- nothing else of theirs leaves this server.
var forwardRequestHeaders = []string{"Range", "If-Range", "Accept"}

// forwardResponseHeaders are the only origin headers passed back. Set-Cookie
// and the like stay here: the origin's session is the server's, not the
// viewer's.
var forwardResponseHeaders = []string{
	"Content-Type", "Content-Length", "Content-Range", "Accept-Ranges", "Last-Modified", "Etag",
}

// blockedProxyHeaders are headers an addon's proxyHeaders.request may not
// set: hop-by-hop and framing headers, and the ones this server decides.
var blockedProxyHeaders = map[string]bool{
	"Host": true, "Content-Length": true, "Transfer-Encoding": true, "Connection": true,
	"Keep-Alive": true, "Te": true, "Trailer": true, "Upgrade": true,
	"Proxy-Authorization": true, "Proxy-Connection": true, "Range": true, "If-Range": true,
	"Accept-Encoding": true, "Expect": true,
}

// CleanProxyHeaders keeps the addon-supplied headers that are safe to send:
// valid names, no CR/LF in values, nothing from blockedProxyHeaders.
func CleanProxyHeaders(in map[string]string) map[string]string {
	if len(in) == 0 {
		return nil
	}
	out := make(map[string]string, len(in))
	for k, v := range in {
		name := textproto.CanonicalMIMEHeaderKey(strings.TrimSpace(k))
		if name == "" || !validHeaderName(name) || blockedProxyHeaders[name] || strings.HasPrefix(name, "Proxy-") {
			continue
		}
		if strings.ContainsAny(v, "\r\n\x00") {
			continue
		}
		out[name] = v
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

func validHeaderName(s string) bool {
	for _, r := range s {
		if r > 127 || !(r == '-' || r == '_' || r >= '0' && r <= '9' || r >= 'A' && r <= 'Z' || r >= 'a' && r <= 'z') {
			return false
		}
	}
	return true
}

// buildUpstreamHeaders is what the origin is sent: the stored proxy headers,
// then the allow-listed headers of the incoming request.
func buildUpstreamHeaders(proxy map[string]string, incoming http.Header, userAgent string) http.Header {
	h := http.Header{}
	if userAgent != "" {
		h.Set("User-Agent", userAgent)
	}
	for k, v := range CleanProxyHeaders(proxy) {
		h.Set(k, v)
	}
	for _, k := range forwardRequestHeaders {
		if v := incoming.Get(k); v != "" {
			h.Set(k, v)
		}
	}
	// Byte ranges must be the origin's bytes, not a compressed body.
	h.Set("Accept-Encoding", "identity")
	return h
}

// CopyResponseHeaders copies the allow-listed origin headers to dst.
func CopyResponseHeaders(dst, src http.Header) {
	for _, k := range forwardResponseHeaders {
		if v := src.Get(k); v != "" {
			dst.Set(k, v)
		}
	}
}
