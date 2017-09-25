// Copyright 2017 The Kubernetes Authors All rights reserved.
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package main

import (
	"flag"
	"log"
	"os"
	"path/filepath"
	"strings"

	u "k8s.io/release/toolbox/util"
)

var (
	// Flags
	branch      = flag.String("branch", "", "Specify a branch other than the current one")
	endDate     = flag.String("end_date", "", "End date")
	githubToken = flag.String("github-token", "", "Must be specified")
	htmlFile    = flag.String("html-file", "", "Produce a html version of the notes")
	mdFile      = flag.String("markdown-file", "", "Specify an alt file to use to store notes")
	order       = flag.String("order", "desc", "The sort order if sort parameter is provided. One of asc or desc.")
	org         = flag.String("user", "kubernetes", "Github owner or org")
	output      = flag.String("output", "./", "Path to output file")
	repo        = flag.String("repo", "", "Github repo")
	sort        = flag.String("sort", "create", "The sort field. Can be comments, created, or updated.")
	startDate   = flag.String("start_date", "", "Start date")
	version     = flag.String("version", "", "Release version")
)

func main() {
	// Parse flags and program parameters
	flag.Parse()
	branchRange := flag.Arg(0)
	progPath := strings.Split(os.Args[0], "/")
	prog := progPath[len(progPath)-1]

	// If branch is not specified in program flag, use current branch
	if *branch == "" {
		var err error
		*branch, err = u.GetCurrentBranch(*branch)
		if err != nil {
			log.Printf("Not a git repository!")
			return
		}
	}

	// Generate output file path for temporary generated release note and PR notes
	prNotes := "/tmp/" + prog + "-" + *branch + "-prnotes"
	if *mdFile != "" {
		*mdFile, _ = filepath.Abs(*mdFile)
	} else {
		*mdFile = "/tmp/" + prog + "-" + *branch + ".md"
	}
	if *htmlFile != "" {
		*htmlFile, _ = filepath.Abs(*htmlFile)
	}
	log.Printf("File paths: %s|%s|%s", prNotes, *mdFile, *htmlFile)

	// Determine range
	generatedBranchRange, err := u.DetermineBranchRange(*branch, branchRange, *org, "kubernetes", *githubToken)
	if err != nil {
		log.Printf("Failed to determine branch range: %s", err)
		return
	}

	// Get PR from git log for specified branch and range
	prs, err := u.FetchPRFromLog(generatedBranchRange)
	if err != nil {
		log.Printf("Failed to get PRs from log: %s", err)
		return
	}

	log.Printf("#PRs from git log: %v.", len(prs))

	// Get PR from github API for specified labels
	log.Printf("Scanning release-note PR label on the %v branch...", *branch)
	releaseNotePRs, err := u.FetchPRByLabel("release-note", *org, *repo, *githubToken, *sort, *order)
	if err != nil {
		log.Printf("Failed to fetch PR with release note label for %s: %s", *repo, err)
	}

	log.Printf("#PRs from github label: %v.", len(releaseNotePRs))

	// Get PRs matching both git log and github labels
	notesReleaseNote := make([]string, 0)
	for _, pr := range prs {
		if releaseNotePRs[pr] {
			notesReleaseNote = append(notesReleaseNote, pr)
		}
	}
	log.Printf("#Final release note PRs: %v.", len(notesReleaseNote))

	return
}
