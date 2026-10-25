package netproxy

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/usememos/memogram/internal/testproxy"
)

func TestProxyValidationDoesNotExposeCredentials(t *testing.T) {
	for _, value := range []string{"", "direct", "http://proxy.test:8080", "https://proxy.test", "socks5://user:pass@proxy.test:1080", "socks5h://[::1]:1080/"} {
		if _, err := Parse(value); err != nil {
			t.Errorf("rejected %q: %v", value, err)
		}
	}
	for _, value := range []string{"proxy.test:8080", "ftp://user:SECRET@proxy.test", "http://proxy.test:65536", "http://proxy.test:0", "http://proxy.test:", "http://user:SECRET@proxy.test/path", "socks5://user:SECRET@proxy.test?x=1", "http://user:SECRET@proxy.test#fragment", "http://user:SECRET@proxy.test:%ZZ", "socks5://:SECRET@proxy.test"} {
		if _, err := Parse(value); err == nil || strings.Contains(err.Error(), "SECRET") || strings.Contains(err.Error(), "proxy.test") {
			t.Errorf("invalid proxy accepted or leaked: %v", err)
		}
	}
}

func TestHTTPProxyAuthenticationAndDirectOverride(t *testing.T) {
	proxy := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !r.URL.IsAbs() || r.URL.Host != "destination.invalid" || r.Header.Get("Proxy-Authorization") != "Basic "+base64.StdEncoding.EncodeToString([]byte("user:pass")) {
			t.Errorf("unexpected proxy request: %s, auth=%t", r.URL, r.Header.Get("Proxy-Authorization") != "")
		}
		fmt.Fprint(w, "proxied")
	}))
	defer proxy.Close()
	u, _ := url.Parse(proxy.URL)
	u.User = url.UserPassword("user", "pass")
	transport, err := Transport(u.String())
	if err != nil {
		t.Fatal(err)
	}
	client := &http.Client{Transport: transport, Timeout: time.Second}
	defer client.CloseIdleConnections()
	resp, err := client.Get("http://destination.invalid/path")
	if err != nil {
		t.Fatal(err)
	}
	data, _ := io.ReadAll(resp.Body)
	_ = resp.Body.Close()
	if string(data) != "proxied" {
		t.Fatalf("request bypassed proxy: %q", data)
	}
	direct, err := Transport("direct")
	if err != nil {
		t.Fatal(err)
	}
	if direct.(*http.Transport).Proxy != nil || http.DefaultTransport.(*http.Transport).Proxy == nil {
		t.Fatal("direct mode uses environment proxies or changed the global transport")
	}
	if inherited, err := Transport(""); err != nil || inherited != http.DefaultTransport {
		t.Fatal("unset proxy changed existing environment behavior")
	}
}

func TestSOCKS5RemoteDNSAndAuthentication(t *testing.T) {
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Proxy-Authorization") != "" {
			t.Error("proxy credentials reached destination")
		}
		fmt.Fprint(w, "through socks")
	}))
	defer backend.Close()
	for _, scheme := range []string{"socks5", "socks5h"} {
		t.Run(scheme, func(t *testing.T) {
			proxy := testproxy.NewSOCKS5(t, strings.TrimPrefix(backend.URL, "http://"), true)
			u, _ := url.Parse(proxy.URL)
			u.Scheme, u.User = scheme, url.UserPassword("user", "p@ss")
			transport, err := Transport(u.String())
			if err != nil {
				t.Fatal(err)
			}
			client := &http.Client{Transport: transport, Timeout: time.Second}
			defer client.CloseIdleConnections()
			resp, err := client.Get("http://remote-dns.invalid:8080/path")
			if err != nil {
				t.Fatal(err)
			}
			data, _ := io.ReadAll(resp.Body)
			_ = resp.Body.Close()
			targets, users, passwords := proxy.Snapshot()
			if string(data) != "through socks" || len(targets) != 1 || targets[0] != "remote-dns.invalid:8080" || users[0] != "user" || passwords[0] != "p@ss" {
				t.Fatalf("SOCKS routing/authentication failed: body=%q targets=%v", data, targets)
			}
		})
	}
}

func TestHTTPConnectKeepsTLSVerification(t *testing.T) {
	backend := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { fmt.Fprint(w, "secure") }))
	defer backend.Close()
	proxy := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodConnect {
			t.Errorf("expected CONNECT, got %s", r.Method)
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		upstream, err := net.DialTimeout("tcp", r.Host, time.Second)
		if err != nil {
			t.Error(err)
			return
		}
		defer func() { _ = upstream.Close() }()
		conn, _, err := w.(http.Hijacker).Hijack()
		if err != nil {
			t.Error(err)
			return
		}
		defer func() { _ = conn.Close() }()
		_ = conn.SetDeadline(time.Now().Add(3 * time.Second))
		_, _ = fmt.Fprint(conn, "HTTP/1.1 200 Connection Established\r\n\r\n")
		done := make(chan struct{})
		go func() { _, _ = io.Copy(upstream, conn); _ = upstream.Close(); close(done) }()
		_, _ = io.Copy(conn, upstream)
		_ = conn.Close()
		<-done
	}))
	defer proxy.Close()
	transport, err := Transport(proxy.URL)
	if err != nil {
		t.Fatal(err)
	}
	client := &http.Client{Transport: transport, Timeout: time.Second}
	defer client.CloseIdleConnections()
	// The self-signed backend must still fail until its certificate is trusted.
	if resp, err := client.Get(backend.URL); err == nil {
		_ = resp.Body.Close()
		t.Fatal("proxy disabled TLS certificate verification")
	}
	trustedTransport := transport.(*http.Transport).Clone()
	trustedTransport.TLSClientConfig = backend.Client().Transport.(*http.Transport).TLSClientConfig.Clone()
	client = &http.Client{Transport: trustedTransport, Timeout: time.Second}
	defer client.CloseIdleConnections()
	resp, err := client.Get(backend.URL)
	if err != nil {
		t.Fatal(err)
	}
	data, _ := io.ReadAll(resp.Body)
	_ = resp.Body.Close()
	if string(data) != "secure" {
		t.Fatalf("CONNECT did not tunnel HTTPS: %q", data)
	}
}

func TestProxyRequestHonorsCancellation(t *testing.T) {
	proxy := httptest.NewServer(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) { <-r.Context().Done() }))
	defer proxy.Close()
	transport, err := Transport(proxy.URL)
	if err != nil {
		t.Fatal(err)
	}
	client := &http.Client{Transport: transport}
	defer client.CloseIdleConnections()
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, "http://target.invalid", nil)
	resp, err := client.Do(req)
	if err == nil {
		_ = resp.Body.Close()
	}
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("proxy ignored context deadline: %v", err)
	}
}
