package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"os"

	"github.com/shakfu/margo/internal/config"
	"github.com/shakfu/margo/pkg/margo"
	"github.com/shakfu/margo/pkg/margo/providers/anthropic"
	"github.com/shakfu/margo/pkg/margo/providers/openai"
)

func main() {
	provider := flag.String("provider", "anthropic", "provider: anthropic | openai")
	prompt := flag.String("prompt", "What is a quaternion?", "prompt to send")
	flag.Parse()

	cfg, err := config.Load()
	if err != nil {
		log.Fatalf("config: %v", err)
	}
	if err := cfg.Validate(); err != nil {
		log.Fatalf("config: %v", err)
	}

	var client margo.Client
	switch *provider {
	case "anthropic":
		if cfg.AnthropicAPIKey == "" {
			log.Fatal("ANTHROPIC_API_KEY not set")
		}
		client = anthropic.New(cfg.AnthropicAPIKey)
	case "openai":
		if cfg.OpenAIAPIKey == "" {
			log.Fatal("OPENAI_API_KEY not set")
		}
		client = openai.New(cfg.OpenAIAPIKey)
	default:
		fmt.Fprintf(os.Stderr, "unknown provider: %s\n", *provider)
		os.Exit(2)
	}

	resp, err := client.Complete(context.Background(), margo.Request{Prompt: *prompt})
	if err != nil {
		log.Fatalf("%s: %v", client.Name(), err)
	}
	fmt.Println(resp.Text)
}
