// Command jobpile pulls remote job listings onto local disk, filters
// them with plain rules, asks a cheap model to catch hidden blockers,
// and writes three local files: apply.md, maybe.md and skip.md.
//
// jobpile never applies, never messages anyone and never posts
// anything. See docs/superpowers/specs/2026-10-12-jobpile-design.md
// for the full design, and README.md for how to run it.
package main

import (
	"fmt"
	"os"
)

func main() {
	if len(os.Args) < 2 {
		usage()
		os.Exit(2)
	}

	var err error
	switch os.Args[1] {
	case "fetch":
		err = cmdFetch(os.Args[2:])
	case "match":
		err = cmdMatch(os.Args[2:])
	case "judge":
		err = cmdJudge(os.Args[2:])
	case "list":
		err = cmdList(os.Args[2:])
	case "run":
		err = cmdRun(os.Args[2:])
	case "-h", "--help", "help":
		usage()
		return
	default:
		fmt.Fprintf(os.Stderr, "jobpile: unknown command %q\n\n", os.Args[1])
		usage()
		os.Exit(2)
	}

	if err != nil {
		fmt.Fprintf(os.Stderr, "jobpile: %v\n", err)
		os.Exit(1)
	}
}

func usage() {
	fmt.Fprint(os.Stderr, `jobpile: find a remote job and leave the applying to a human.

Usage:
  jobpile fetch  [--data DIR]              pull remoet and Himalayas into jobs.db
  jobpile match  [--data DIR]              apply the rules, mark pass/skip
  jobpile judge  [--data DIR] [--max N]    run the model on new passing jobs
  jobpile list   [--data DIR]              write apply.md, maybe.md, skip.md
  jobpile run    [--data DIR] [--max N]    fetch, match, judge, list in order

DIR may also be set with the JOBPILE_DATA environment variable.
See README.md for how to set up a data folder.
`)
}
