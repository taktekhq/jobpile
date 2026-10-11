package rules

import (
	"math"
	"testing"

	"github.com/taktekhq/jobpile/internal/model"
)

func testTitleWords() TitleWords {
	return TitleWords{
		SeniorPatterns:   []string{"staff", "principal", "senior", "lead", "architect", "engineering manager", "head of engineering", "founding engineer"},
		EngineeringWords: []string{"engineer", "developer", "architect", "engineering"},
		ExcludePatterns:  []string{"junior", "intern", "frontend", "front-end", "ios", "android", "mobile", "data scientist", "sales engineer", "support", "qa"},
	}
}

func testRegionWords() RegionWords {
	return RegionWords{
		Tier1Keywords:  []string{"worldwide", "global", "anywhere", "emea", "mena", "middle east"},
		Tier1Countries: []string{"Lebanon", "United Arab Emirates", "Saudi Arabia", "Jordan", "Egypt", "Turkey", "Cyprus"},
		Tier2Keywords:  []string{"united states", "usa", "us", "u.s.", "u.s.a."},
	}
}

func TestMatchTitle(t *testing.T) {
	w := testTitleWords()
	cases := []struct {
		title    string
		wantPass bool
	}{
		{"Staff Backend Engineer", true},
		{"Principal Software Engineer", true},
		{"Senior Software Engineer", true},
		{"Solutions Architect", true},
		{"Engineering Manager", true},
		{"Founding Engineer", true},
		{"Head of Engineering", true},
		{"Junior Backend Engineer", false},
		{"Software Engineering Intern", false},
		{"Senior Frontend Engineer", false},
		{"Senior iOS Engineer", false},
		{"Senior Android Developer", false},
		{"Staff Data Scientist", false},
		{"Senior Sales Engineer", false},
		{"Technical Support Engineer", false},
		{"Senior QA Engineer", false},
		{"Product Manager", false},
		{"Tech Lead", false}, // "lead" matches senior, but no engineering word in the bare title
	}
	for _, c := range cases {
		pass, reason := MatchTitle(c.title, w)
		if pass != c.wantPass {
			t.Errorf("MatchTitle(%q) = %v (%s), want %v", c.title, pass, reason, c.wantPass)
		}
	}
}

func TestClassifyLocation(t *testing.T) {
	w := testRegionWords()
	cases := []struct {
		name         string
		remotePolicy string
		regions      []string
		wantTier     int
		wantPass     bool
	}{
		{"worldwide no restrictions", "remote", nil, 1, true},
		{"explicit worldwide keyword", "remote-restricted", []string{"Worldwide"}, 1, true},
		{"includes Lebanon", "remote-restricted", []string{"Lebanon", "France"}, 1, true},
		{"EMEA keyword", "remote-restricted", []string{"EMEA"}, 1, true},
		{"US only", "remote-restricted", []string{"United States"}, 2, true},
		{"US only lowercase", "remote-restricted", []string{"us"}, 2, true},
		{"EU only", "remote-restricted", []string{"France", "Germany", "Spain", "United Kingdom", "Italy"}, 0, false},
		{"India only", "remote-restricted", []string{"India"}, 0, false},
		{"hybrid", "hybrid", nil, 0, false},
		{"onsite", "onsite", nil, 0, false},
	}
	for _, c := range cases {
		tier, pass, reason := ClassifyLocation(c.remotePolicy, c.regions, w)
		if tier != c.wantTier || pass != c.wantPass {
			t.Errorf("%s: ClassifyLocation(%q, %v) = tier %d pass %v (%s), want tier %d pass %v",
				c.name, c.remotePolicy, c.regions, tier, pass, reason, c.wantTier, c.wantPass)
		}
	}
}

func TestConvertToYearlyUSD(t *testing.T) {
	rates := map[string]float64{"USD": 1, "EUR": 0.92, "GBP": 0.79}
	cases := []struct {
		name     string
		amount   float64
		currency string
		period   string
		want     float64
	}{
		{"usd yearly", 100000, "USD", "year", 100000},
		{"usd monthly", 10000, "USD", "month", 120000},
		{"usd hourly", 50, "USD", "hour", 104000},
		{"eur yearly", 92000, "EUR", "year", 100000},
		{"default period is yearly", 80000, "USD", "", 80000},
	}
	for _, c := range cases {
		got, err := ConvertToYearlyUSD(c.amount, c.currency, c.period, rates)
		if err != nil {
			t.Fatalf("%s: unexpected error: %v", c.name, err)
		}
		if math.Abs(got-c.want) > 0.01 {
			t.Errorf("%s: got %v, want %v", c.name, got, c.want)
		}
	}
}

func TestConvertToYearlyUSDErrors(t *testing.T) {
	rates := map[string]float64{"USD": 1}
	if _, err := ConvertToYearlyUSD(100, "XYZ", "year", rates); err == nil {
		t.Error("unknown currency: want error, got nil")
	}
	if _, err := ConvertToYearlyUSD(100, "USD", "fortnight", rates); err == nil {
		t.Error("unknown period: want error, got nil")
	}
}

func TestCheckSalary(t *testing.T) {
	floor := 80000.0
	low := 70000.0
	high := 150000.0
	exactFloor := 80000.0

	if pass, _ := CheckSalary(nil, floor); !pass {
		t.Error("no salary listed: want pass")
	}
	if pass, _ := CheckSalary(&low, floor); pass {
		t.Error("salary below floor: want skip")
	}
	if pass, _ := CheckSalary(&high, floor); !pass {
		t.Error("salary above floor: want pass")
	}
	if pass, _ := CheckSalary(&exactFloor, floor); !pass {
		t.Error("salary exactly at floor: want pass")
	}
}

func TestEvaluateStopsAtFirstFailure(t *testing.T) {
	titleWords := testTitleWords()
	regionWords := testRegionWords()

	job := &model.Job{Title: "Junior Backend Engineer", RemotePolicy: "remote"}
	verdict, reason := Evaluate(job, titleWords, regionWords, 80000)
	if verdict != "skip" || reason == "" {
		t.Errorf("junior title: got verdict %q reason %q, want skip with a reason", verdict, reason)
	}
	if job.Tier != 0 {
		t.Errorf("junior title: Tier = %d, want 0 (location never checked)", job.Tier)
	}

	low := 50000.0
	job2 := &model.Job{Title: "Staff Backend Engineer", RemotePolicy: "remote", SalaryMaxUSD: &low}
	verdict2, _ := Evaluate(job2, titleWords, regionWords, 80000)
	if verdict2 != "skip" {
		t.Errorf("low salary: got verdict %q, want skip", verdict2)
	}
	if job2.Tier != 1 {
		t.Errorf("low salary: Tier = %d, want 1 (location passed before salary check)", job2.Tier)
	}

	job3 := &model.Job{Title: "Staff Backend Engineer", RemotePolicy: "remote"}
	verdict3, _ := Evaluate(job3, titleWords, regionWords, 80000)
	if verdict3 != "pass" {
		t.Errorf("clean pass: got verdict %q, want pass", verdict3)
	}
	if job3.Tier != 1 {
		t.Errorf("clean pass: Tier = %d, want 1", job3.Tier)
	}
}
