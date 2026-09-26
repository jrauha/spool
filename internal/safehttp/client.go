package safehttp

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"time"
)

func NewClient(timeout time.Duration) *http.Client {
	return &http.Client{
		Timeout:   timeout,
		Transport: &http.Transport{Proxy: nil, DialContext: dialContext},
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			if !ValidURL(req.URL) {
				return fmt.Errorf("unsafe HTTP redirect")
			}
			return nil
		},
	}
}

func ValidURL(parsedURL *url.URL) bool {
	if parsedURL == nil || parsedURL.Host == "" || parsedURL.User != nil ||
		(parsedURL.Scheme != "http" && parsedURL.Scheme != "https") {
		return false
	}
	if ip := net.ParseIP(parsedURL.Hostname()); ip != nil && !isPublicIP(ip) {
		return false
	}
	port := parsedURL.Port()
	return port == "" || port == "80" || port == "443"
}

func dialContext(ctx context.Context, network, address string) (net.Conn, error) {
	host, port, err := net.SplitHostPort(address)
	if err != nil {
		return nil, err
	}
	addresses, err := net.DefaultResolver.LookupNetIP(ctx, "ip", host)
	if err != nil {
		return nil, fmt.Errorf("resolve HTTP host: %w", err)
	}
	if len(addresses) == 0 {
		return nil, fmt.Errorf("HTTP host has no addresses")
	}
	for _, ip := range addresses {
		if !isPublicIP(net.IP(ip.AsSlice())) {
			return nil, fmt.Errorf("HTTP host resolves to a non-public address")
		}
	}
	return (&net.Dialer{}).DialContext(ctx, network, net.JoinHostPort(addresses[0].String(), port))
}

func isPublicIP(ip net.IP) bool {
	return !ip.IsLoopback() && !ip.IsPrivate() && !ip.IsLinkLocalUnicast() &&
		!ip.IsLinkLocalMulticast() && !ip.IsMulticast() && !ip.IsUnspecified()
}
