package scanner

import (
	"fmt"
	"strings"
	"time"

	"github.com/caarlos0/env/v11"
)

const NetworkDisabled = "disabled"

//go:generate go tool envdoc -types Environment -output environment.md -format markdown
//go:generate go run ./envdoc_normalize.go environment.md
type Environment struct {
	// Scanner network mode. Only disabled is accepted; validation and provider network access never run.
	Network string `env:"TALARIA_SCANNER_NETWORK,notEmpty" envDefault:"disabled"`
}

func ParseEnvironment(values map[string]string) (Environment, error) {
	if value, exists := values["TALARIA_SCANNER_NETWORK"]; exists && value == "" {
		return Environment{}, fmt.Errorf("TALARIA_SCANNER_NETWORK must not be empty")
	}
	configuration, err := env.ParseAsWithOptions[Environment](env.Options{Environment: values})
	if err != nil {
		return Environment{}, fmt.Errorf("parse scanner environment: %w", err)
	}
	if err := configuration.Validate(); err != nil {
		return Environment{}, err
	}
	return configuration, nil
}

func (configuration Environment) Validate() error {
	if configuration.Network != NetworkDisabled {
		return fmt.Errorf("TALARIA_SCANNER_NETWORK must be disabled")
	}
	return nil
}

type Config struct {
	Generation string
	Timeout    time.Duration
}

func (configuration Config) validate() error {
	if strings.TrimSpace(configuration.Generation) == "" || len(configuration.Generation) > 128 {
		return fmt.Errorf("scanner generation is invalid")
	}
	if configuration.Timeout <= 0 {
		return fmt.Errorf("scanner timeout must be positive")
	}
	return nil
}
