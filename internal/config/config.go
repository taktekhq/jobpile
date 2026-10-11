// Package config reads jobpile's config.toml: the floor, the currency
// table, the title and region patterns, the sources, and the model
// command the judge runs. Nothing here talks to a network or a shell; it
// only parses the file.
package config

import (
	"fmt"
	"os"
	"strings"

	"github.com/BurntSushi/toml"
)

// Config is the whole of config.toml.
type Config struct {
	Floor float64 `toml:"floor_usd"`

	// CurrencyRates maps an ISO currency code to how many of that
	// currency equal one USD. USD itself should be 1.
	CurrencyRates map[string]float64 `toml:"currency_rates"`

	Title  TitleConfig  `toml:"title"`
	Region RegionConfig `toml:"region"`

	Remoet    RemoetConfig    `toml:"remoet"`
	Himalayas HimalayasConfig `toml:"himalayas"`

	Judge JudgeConfig `toml:"judge"`
}

// TitleConfig holds the word lists the title rule matches against.
type TitleConfig struct {
	SeniorPatterns   []string `toml:"senior_patterns"`
	EngineeringWords []string `toml:"engineering_words"`
	ExcludePatterns  []string `toml:"exclude_patterns"`
}

// RegionConfig holds the word and country lists the location-tier rule
// matches against.
type RegionConfig struct {
	Tier1Keywords  []string `toml:"tier1_keywords"`
	Tier1Countries []string `toml:"tier1_countries"`
	Tier2Keywords  []string `toml:"tier2_keywords"`
}

// RemoetConfig configures the remoet.dev MCP source.
type RemoetConfig struct {
	BaseURL string `toml:"base_url"`
	// KeyCommand is a shell command whose stdout is the API key. Used
	// when REMOET_API_KEY is not set in the environment.
	KeyCommand       string `toml:"key_command"`
	RequestDelayMS   int    `toml:"request_delay_ms"`
	MaxCallsPerRun   int    `toml:"max_calls_per_run"`
	BudgetStopBefore int    `toml:"budget_stop_before"`
}

// HimalayasConfig configures the Himalayas public API source.
type HimalayasConfig struct {
	BaseURL        string `toml:"base_url"`
	RequestDelayMS int    `toml:"request_delay_ms"`
	MaxPages       int    `toml:"max_pages"`
	// Queries is the set of title keyword searches jobpile runs against
	// the Himalayas search endpoint instead of crawling its full ~120k
	// job feed. See internal/sources/himalayas's package comment.
	Queries []string `toml:"queries"`
	// SeniorityFilter is passed as the search endpoint's seniority
	// parameter on every query.
	SeniorityFilter []string `toml:"seniority_filter"`
}

// JudgeConfig configures the model the judge subcommand runs.
type JudgeConfig struct {
	// Command is the model command, split on spaces unless quoted, e.g.
	// `claude -p --model haiku --tools ""`.
	Command     string `toml:"command"`
	MaxPerRun   int    `toml:"max_per_run"`
	Concurrency int    `toml:"concurrency"`
}

// Load reads and parses a config.toml file at path, then fills in any
// defaults that were left blank.
func Load(path string) (*Config, error) {
	var c Config
	if _, err := toml.DecodeFile(path, &c); err != nil {
		return nil, fmt.Errorf("reading config %s: %w", path, err)
	}
	c.applyDefaults()
	if err := c.validate(); err != nil {
		return nil, err
	}
	return &c, nil
}

func (c *Config) applyDefaults() {
	if c.Floor == 0 {
		c.Floor = 80000
	}
	if c.CurrencyRates == nil {
		c.CurrencyRates = map[string]float64{}
	}
	if _, ok := c.CurrencyRates["USD"]; !ok {
		c.CurrencyRates["USD"] = 1
	}
	if c.Remoet.RequestDelayMS == 0 {
		c.Remoet.RequestDelayMS = 300
	}
	if c.Remoet.BudgetStopBefore == 0 {
		c.Remoet.BudgetStopBefore = 4000
	}
	if c.Himalayas.RequestDelayMS == 0 {
		c.Himalayas.RequestDelayMS = 300
	}
	if c.Himalayas.MaxPages == 0 {
		c.Himalayas.MaxPages = 5
	}
	if len(c.Himalayas.Queries) == 0 {
		c.Himalayas.Queries = []string{
			"staff engineer", "principal engineer", "software architect",
			"engineering manager", "founding engineer", "lead engineer", "tech lead",
		}
	}
	if len(c.Himalayas.SeniorityFilter) == 0 {
		c.Himalayas.SeniorityFilter = []string{"Senior", "Manager", "Director", "Executive"}
	}
	if c.Judge.Command == "" {
		c.Judge.Command = `claude -p --model haiku --tools ""`
	}
	if c.Judge.MaxPerRun == 0 {
		c.Judge.MaxPerRun = 150
	}
	if c.Judge.Concurrency == 0 {
		c.Judge.Concurrency = 4
	}
}

func (c *Config) validate() error {
	if c.Floor < 0 {
		return fmt.Errorf("config: floor_usd must not be negative")
	}
	return nil
}

// RemoetAPIKey resolves the remoet API key: the REMOET_API_KEY
// environment variable wins, otherwise it runs KeyCommand and reads its
// stdout. It never prints the key; the caller must not print it either.
func (r RemoetConfig) RemoetAPIKey(runKeyCommand func(string) (string, error)) (string, error) {
	if v := os.Getenv("REMOET_API_KEY"); v != "" {
		return strings.TrimSpace(v), nil
	}
	if r.KeyCommand == "" {
		return "", fmt.Errorf("remoet: no REMOET_API_KEY and no key_command in config")
	}
	out, err := runKeyCommand(r.KeyCommand)
	if err != nil {
		return "", fmt.Errorf("remoet: key_command failed: %w", err)
	}
	key := strings.TrimSpace(out)
	if key == "" {
		return "", fmt.Errorf("remoet: key_command produced an empty key")
	}
	return key, nil
}
