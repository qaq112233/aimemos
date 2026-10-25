// Package netproxy configures HTTP and SOCKS5 proxies for outbound clients.
package netproxy

import (
	"errors"
	"net/http"
	"net/url"
	"strconv"
	"strings"
)

// Parse accepts an explicit proxy URL, "direct", or an empty setting. Errors
// deliberately omit the supplied URL because it may contain credentials.
func Parse(value string) (*url.URL, error) {
	value = strings.TrimSpace(value)
	if value == "" || value == "direct" {
		return nil, nil
	}
	u, err := url.Parse(value)
	if err != nil || u.Hostname() == "" || strings.HasSuffix(u.Host, ":") || u.Opaque != "" || (u.Path != "" && u.Path != "/") || u.RawQuery != "" || u.ForceQuery || u.Fragment != "" {
		return nil, errors.New("proxy must be an HTTP(S) or SOCKS5 URL without a path, query or fragment, or direct")
	}
	switch u.Scheme {
	case "http", "https", "socks5", "socks5h":
	default:
		return nil, errors.New("proxy scheme must be http, https, socks5 or socks5h")
	}
	if port := u.Port(); port != "" {
		n, err := strconv.Atoi(port)
		if err != nil || n <= 0 || n > 65535 {
			return nil, errors.New("proxy port must be between 1 and 65535")
		}
	}
	if (u.Scheme == "socks5" || u.Scheme == "socks5h") && u.User != nil {
		password, _ := u.User.Password()
		if len(u.User.Username()) == 0 || len(u.User.Username()) > 255 || len(password) > 255 {
			return nil, errors.New("SOCKS5 username must be 1-255 bytes and password at most 255 bytes")
		}
	}
	return u, nil
}

// Transport preserves the existing environment proxy behavior for an empty
// setting. Explicit settings override HTTP_PROXY/HTTPS_PROXY/NO_PROXY, including
// loopback destinations. Each caller owns its transport; globals are unchanged.
func Transport(value string) (http.RoundTripper, error) {
	u, err := Parse(value)
	if err != nil {
		return nil, err
	}
	if strings.TrimSpace(value) == "" {
		return http.DefaultTransport, nil
	}
	base, ok := http.DefaultTransport.(*http.Transport)
	if !ok {
		return nil, errors.New("default HTTP transport does not support proxy configuration")
	}
	transport := base.Clone()
	transport.Proxy = nil
	if u != nil {
		transport.Proxy = http.ProxyURL(u)
	}
	return transport, nil
}
