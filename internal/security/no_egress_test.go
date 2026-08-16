package security

import (
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestNoEgressSource(t *testing.T) {
	for _, directory := range []string{"."} {
		err := filepath.WalkDir(directory, func(path string, entry os.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".go") || strings.HasSuffix(entry.Name(), "_test.go") {
				return nil
			}
			contents, err := os.ReadFile(path)
			if err != nil {
				return err
			}
			for _, forbidden := range []string{
				"http.Client{", "http.DefaultClient", "http.Get(", "http.Post(",
				"net.Dial(", "net.Dialer", "tls.Dial", "http.Transport",
			} {
				if strings.Contains(string(contents), forbidden) {
					t.Errorf("source %s contains outbound primitive %s", path, forbidden)
				}
			}
			return nil
		})
		if err != nil {
			t.Fatal(err)
		}
	}
}

func TestNoEgressProcessBoundary(t *testing.T) {
	if os.Getenv("TALARIA_SECURITY_PROCESS_HELPER") == "1" {
		token, err := GenerateBearerToken()
		if err != nil {
			os.Exit(2)
		}
		authenticator, err := NewAuthenticator(AuthConfig{Token: token, Host: "127.0.0.1:7331"})
		if err != nil || authenticator == nil {
			os.Exit(3)
		}
		request := validRequest(token)
		if err := authenticator.Authenticate(request); err != nil {
			os.Exit(4)
		}
		return
	}

	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	connection := make(chan struct{}, 1)
	go func() {
		conn, err := listener.Accept()
		if err == nil {
			_ = conn.Close()
			connection <- struct{}{}
		}
	}()
	proxy := "http://" + listener.Addr().String()
	command := exec.Command(os.Args[0], "-test.run=^TestNoEgressProcessBoundary$", "-test.count=1")
	command.Env = append(os.Environ(),
		"TALARIA_SECURITY_PROCESS_HELPER=1",
		"HTTP_PROXY="+proxy,
		"HTTPS_PROXY="+proxy,
		"ALL_PROXY="+proxy,
		"NO_PROXY=",
	)
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("security subprocess failed: %v; output=%s", err, output)
	}
	select {
	case <-connection:
		t.Fatal("security package attempted outbound connection")
	case <-time.After(250 * time.Millisecond):
	}
}
