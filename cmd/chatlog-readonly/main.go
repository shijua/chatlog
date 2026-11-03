package main

import (
	"flag"
	"io"
	"log"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"strings"
	"time"
)

const upstream = "http://127.0.0.1:5030"

var allowedQueries = map[string]map[string]bool{
	"/api/v1/timeline": {"start": true, "end": true, "limit": true, "cursor": true, "format": true, "exclude_talker": true, "exclude_sender": true},
	"/api/v1/session":  {"keyword": true, "limit": true, "offset": true, "format": true},
	"/api/v1/chatlog":  {"talker": true, "time": true, "sender": true, "keyword": true, "limit": true, "offset": true, "format": true},
	"/api/v1/contact":  {"keyword": true, "limit": true, "offset": true, "format": true},
	"/api/v1/chatroom": {"keyword": true, "limit": true, "offset": true, "format": true},
}

func readOnlyHandler(client *http.Client) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		w.Header().Set("X-Content-Type-Options", "nosniff")
		if r.Method != http.MethodGet {
			w.Header().Set("Allow", http.MethodGet)
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		isImage := strings.HasPrefix(r.URL.Path, "/image/")
		allowed, ok := allowedQueries[r.URL.Path]
		if isImage {
			allowed = map[string]bool{}
			ok = validImageKey(strings.TrimPrefix(r.URL.Path, "/image/"))
		}
		if !ok || r.URL.EscapedPath() != r.URL.Path || r.URL.IsAbs() || r.URL.Host != "" {
			http.NotFound(w, r)
			return
		}
		if r.ContentLength != 0 || len(r.TransferEncoding) != 0 {
			http.Error(w, "request body not allowed", http.StatusBadRequest)
			return
		}
		query, err := url.ParseQuery(r.URL.RawQuery)
		if err != nil {
			http.Error(w, "invalid query", http.StatusBadRequest)
			return
		}
		for key, values := range query {
			if !allowed[key] || len(values) != 1 {
				http.Error(w, "query parameter not allowed", http.StatusBadRequest)
				return
			}
		}
		if isImage {
			serveImage(w, r, client)
			return
		}
		if format := query.Get("format"); format != "" && format != "json" {
			http.Error(w, "only JSON is supported", http.StatusBadRequest)
			return
		}
		query.Set("format", "json")
		// Build a fresh request so client headers and bodies cannot reach chatlog.
		req, err := http.NewRequestWithContext(r.Context(), http.MethodGet, upstream+r.URL.Path+"?"+query.Encode(), nil)
		if err != nil {
			http.Error(w, "invalid request", http.StatusBadRequest)
			return
		}
		resp, err := client.Do(req)
		if err != nil {
			http.Error(w, "backend unavailable", http.StatusBadGateway)
			return
		}
		defer resp.Body.Close()
		if resp.StatusCode >= 300 && resp.StatusCode < 400 {
			http.Error(w, "backend redirect rejected", http.StatusBadGateway)
			return
		}
		w.Header().Set("Content-Type", resp.Header.Get("Content-Type"))
		w.WriteHeader(resp.StatusCode)
		if _, err := io.Copy(w, resp.Body); err != nil {
			log.Print("response transfer interrupted")
		}
	})
}

func validListenAddr(addr string) bool {
	host, _, err := net.SplitHostPort(addr)
	if err != nil {
		return false
	}
	ip, err := netip.ParseAddr(host)
	return err == nil && (ip.IsLoopback() || netip.MustParsePrefix("100.64.0.0/10").Contains(ip))
}

func peerOnlyHandler(next http.Handler, peer netip.Addr) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		host, _, err := net.SplitHostPort(r.RemoteAddr)
		ip, parseErr := netip.ParseAddr(host)
		if err != nil || parseErr != nil || ip.Unmap() != peer.Unmap() {
			http.Error(w, "source not allowed", http.StatusForbidden)
			return
		}
		next.ServeHTTP(w, r)
	})
}

func backendClient() *http.Client {
	return &http.Client{
		Timeout:       2 * time.Minute,
		Transport:     &http.Transport{Proxy: nil},
		CheckRedirect: func(_ *http.Request, _ []*http.Request) error { return http.ErrUseLastResponse },
	}
}

func main() {
	addr := flag.String("addr", "127.0.0.1:5031", "loopback or Tailscale IPv4 listen address")
	allowPeer := flag.String("allow-peer", "", "single allowed Tailscale IPv4 source; required for remote access")
	flag.Parse()
	if !validListenAddr(*addr) {
		log.Fatal("listen address must use a loopback or Tailscale IPv4 address")
	}

	handler := readOnlyHandler(backendClient())
	host, _, _ := net.SplitHostPort(*addr)
	listenIP, _ := netip.ParseAddr(host)
	if *allowPeer != "" || !listenIP.IsLoopback() {
		peer, err := netip.ParseAddr(*allowPeer)
		if err != nil || !netip.MustParsePrefix("100.64.0.0/10").Contains(peer) {
			log.Fatal("remote access requires --allow-peer with the intended VM's Tailscale IPv4 address")
		}
		handler = peerOnlyHandler(handler, peer)
	}
	srv := &http.Server{
		Addr: *addr, Handler: handler,
		ReadHeaderTimeout: 5 * time.Second, ReadTimeout: 15 * time.Second,
		WriteTimeout: 3 * time.Minute, IdleTimeout: time.Minute,
		MaxHeaderBytes: 16 * 1024,
	}
	log.Printf("read-only chatlog gateway listening on %s", *addr)
	log.Fatal(srv.ListenAndServe())
}
