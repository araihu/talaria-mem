package config

import "testing"

func TestValidateBaseURLPolicy(t *testing.T) {
	valid := []string{
		"https://api.example.test/v1",
		"http://127.0.0.1:11434/v1",
		"http://127.0.0.42:8080/v1",
		"http://[::1]:11434/v1",
	}
	for _, value := range valid {
		if err := ValidateBaseURL(value); err != nil {
			t.Errorf("ValidateBaseURL(%q) error = %v", value, err)
		}
	}
	invalid := []string{
		"http://localhost:11434/v1",
		"http://example.test/v1",
		"https://api.example.test/v1/",
		"https://user:pass@api.example.test/v1",
		"https://api.example.test/v1?token=secret",
		"https://api.example.test/v1#fragment",
		"ftp://api.example.test/v1",
		"http://192.168.1.2/v1",
	}
	for _, value := range invalid {
		if err := ValidateBaseURL(value); err == nil {
			t.Errorf("ValidateBaseURL(%q) error = nil", value)
		}
	}
}
