package config

import "testing"

// TestLoadExampleConfig makes sure config.example.toml, the file the
// README tells people to copy, actually parses. A typo here would only
// show up the first time someone ran jobpile for real.
func TestLoadExampleConfig(t *testing.T) {
	c, err := Load("../../config.example.toml")
	if err != nil {
		t.Fatalf("Load(config.example.toml): %v", err)
	}
	if c.Floor != 80000 {
		t.Errorf("Floor = %v, want 80000", c.Floor)
	}
	if len(c.Title.SeniorPatterns) == 0 {
		t.Error("Title.SeniorPatterns is empty")
	}
	if len(c.Region.Tier1Countries) == 0 {
		t.Error("Region.Tier1Countries is empty")
	}
	if c.Remoet.KeyCommand == "" {
		t.Error("Remoet.KeyCommand is empty")
	}
	if c.Judge.Command == "" {
		t.Error("Judge.Command is empty")
	}
	if c.Watch.IMAPHost == "" {
		t.Error("Watch.IMAPHost is empty")
	}
	if len(c.Watch.Companies) == 0 {
		t.Error("Watch.Companies is empty")
	}
	if len(c.Watch.IgnoreSubjects) == 0 {
		t.Error("Watch.IgnoreSubjects is empty")
	}
}
