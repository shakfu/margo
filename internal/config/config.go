package config

import (
	"errors"
	"os"

	"github.com/joho/godotenv"
)

type Config struct {
	AnthropicAPIKey string
	OpenAIAPIKey    string
}

// Load reads .env (if present) and returns config from the environment.
// A missing .env file is not an error; missing variables are reported by Validate.
func Load(paths ...string) (*Config, error) {
	_ = godotenv.Load(paths...)
	return &Config{
		AnthropicAPIKey: os.Getenv("ANTHROPIC_API_KEY"),
		OpenAIAPIKey:    os.Getenv("OPENAI_API_KEY"),
	}, nil
}

func (c *Config) Validate() error {
	if c.AnthropicAPIKey == "" && c.OpenAIAPIKey == "" {
		return errors.New("no provider API keys set: need ANTHROPIC_API_KEY and/or OPENAI_API_KEY")
	}
	return nil
}
