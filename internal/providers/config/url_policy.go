package config

import (
	"errors"
	"fmt"
	"net"
	"net/url"
)

func ValidateBaseURL(value string) error {
	parsed, err := url.Parse(value)
	if err != nil {
		return fmt.Errorf("invalid URL: %w", err)
	}
	if parsed.User != nil || parsed.RawQuery != "" || parsed.Fragment != "" || parsed.Opaque != "" {
		return errors.New("URL must not contain userinfo, query, fragment, or opaque data")
	}
	if parsed.Path != "/v1" || parsed.Hostname() == "" || parsed.Port() == "" && parsed.Host == "" {
		return errors.New("URL must contain an explicit /v1 endpoint and host")
	}
	switch parsed.Scheme {
	case "https":
		return nil
	case "http":
		ip := net.ParseIP(parsed.Hostname())
		if ip == nil || !(ip.IsLoopback()) {
			return errors.New("plain HTTP is allowed only for a literal loopback address")
		}
		return nil
	default:
		return fmt.Errorf("unsupported URL scheme %q", parsed.Scheme)
	}
}
