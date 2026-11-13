// Package baseurl resolves the public base URL used for the absolute links in
// generated feeds.
package baseurl

import (
	"net/http"
	"net/url"
	"strings"
)

// maxHostLen is the longest host name an absolute link may carry.
const maxHostLen = 255

// Resolve returns the base URL for the absolute links of a response to req.
//
// The request carries no scheme behind an ingress, so the scheme is taken from a
// well-formed X-Forwarded-Proto first and from the configured public URL second.
// Both values end up verbatim in a published feed, hence the scheme must be http
// or https and the request host must parse as a host before either is used.
// https with the request host is the last resort.
func Resolve(req *http.Request, configured string) *url.URL {
	if proto, ok := forwardedProto(req.Header.Get("X-Forwarded-Proto")); ok && validHost(req.Host) {
		return &url.URL{Scheme: proto, Host: req.Host}
	}

	if u, ok := parseConfigured(configured); ok {
		return u
	}

	return &url.URL{Scheme: "https", Host: req.Host}
}

// Self returns the self link of a feed: base with the path and query of req.
func Self(base *url.URL, req *http.Request) string {
	u := *base
	u.Path = joinPath(base.Path, req.URL.Path)
	u.RawPath = ""
	u.RawQuery = req.URL.RawQuery

	return u.String()
}

// Link returns the absolute link to path below base.
func Link(base *url.URL, path string) string {
	u := *base
	u.Path = joinPath(base.Path, path)
	u.RawPath = ""
	u.RawQuery = ""

	return u.String()
}

// joinPath appends path to the path prefix of the base URL.
func joinPath(prefix, path string) string {
	if prefix == "" || prefix == "/" {
		return path
	}

	return strings.TrimSuffix(prefix, "/") + path
}

// forwardedProto returns the scheme named by the first entry of the header. A
// chain of proxies appends its own values, so the first one is the scheme the
// client used.
func forwardedProto(header string) (string, bool) {
	proto, _, _ := strings.Cut(header, ",")
	switch proto = strings.ToLower(strings.TrimSpace(proto)); proto {
	case "http", "https":
		return proto, true
	}

	return "", false
}

// parseConfigured parses the configured public URL. Values that would produce a
// broken link are ignored so that the request fallback takes over.
func parseConfigured(raw string) (*url.URL, bool) {
	if raw == "" {
		return nil, false
	}

	u, err := url.Parse(raw)
	if err != nil || u.Host == "" || (u.Scheme != "http" && u.Scheme != "https") {
		return nil, false
	}

	return &url.URL{Scheme: u.Scheme, Host: u.Host, Path: u.Path}, true
}

// validHost reports whether host can be the authority of an absolute URL.
func validHost(host string) bool {
	if host == "" || len(host) > maxHostLen {
		return false
	}

	u, err := url.Parse("https://" + host)
	if err != nil {
		return false
	}

	return u.Host == host && u.Path == "" && u.RawQuery == "" && u.Fragment == "" && u.User == nil
}
