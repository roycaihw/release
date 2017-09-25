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

package util

import (
	"fmt"
	"regexp"
	"strings"
)

// GetCurrentBranch gets the current working git branch
func GetCurrentBranch(branch string) (string, error) {
	// TODO: add 2>/dev/null to the shell command to avoid error on empty branch
	branch, err := RunShell("git rev-parse --abbrev-ref HEAD")
	if err != nil {
		return "", err
	}

	// Remove trailing newline
	branch = strings.TrimSpace(branch)
	return branch, nil
}

// FetchPRFromLog gets PR ids from git log within input range
func FetchPRFromLog(generatedBranchRange string) ([]string, error) {
	s := make([]string, 0)

	gitLogCommand := "git log " + generatedBranchRange + " --format=\"%s\" --grep=Merge"
	lines, err := RunShell(gitLogCommand)
	if err != nil {
		return nil, err
	}
	vCherryPick, err := regexp.Compile("automated-cherry-pick-of-#([0-9]+)-{1,}")
	if err != nil {
		return nil, err
	}
	vMergePR, err := regexp.Compile("Merge pull request #([0-9]+) from")
	if err != nil {
		return nil, err
	}
	for _, line := range strings.Split(lines, "\n") {
		if pr := vCherryPick.FindStringSubmatch(line); pr != nil {
			s = append(s, pr[1])
		} else if pr := vMergePR.FindStringSubmatch(line); pr != nil {
			s = append(s, pr[1])
		}
	}

	// Get slice of PRs by executing git log
	return s, nil
}

// DetermineBranchRange determines valid git log range based on input range for input branch
func DetermineBranchRange(currentBranch string, branchRange string, org string, repo string, githubToken string) (string, error) {
	// Determine remote branch head
	gitBranchCommand := "git rev-parse refs/remotes/origin/" + currentBranch
	branchHead, err := RunShell(gitBranchCommand)
	if err != nil {
		return "", err
	}
	branchHead = strings.TrimSpace(branchHead)

	// Last release
	lastRelease, err := LastRelease(org, repo, githubToken)
	if err != nil {
		return "", err
	}

	// If lastRelease[currentBranch] is unset attempt to get the last release from the parent branch
	// and then master
	if idx := strings.LastIndex(currentBranch, "."); lastRelease[currentBranch] == "" && idx != -1 {
		lastRelease[currentBranch] = lastRelease[currentBranch[:idx]]
	}
	if lastRelease[currentBranch] == "" {
		lastRelease[currentBranch] = lastRelease["master"]
	}

	var startTag string
	var releaseTag string

	// Default range
	generatedBranchRange := branchRange
	if branchRange == "" {
		generatedBranchRange = lastRelease[currentBranch] + ".." + branchHead
	}

	// Parse start and release tag
	v, err := regexp.Compile("([v0-9.]*-*(alpha|beta|rc)*\\.*[0-9]*)\\.\\.([v0-9.]*-*(alpha|beta|rc)*\\.*[0-9]*)$")
	if err != nil {
		return "", err
	}
	tags := v.FindStringSubmatch(branchRange)
	if tags != nil {
		startTag = tags[1]
		releaseTag = tags[3]
	} else {
		startTag = lastRelease[currentBranch]
		releaseTag = branchRange
	}
	if startTag == "" {
		panic(fmt.Sprintf("Unable to set beginning of range automatically. Specify on the command-line. Exiting..."))
	}
	if releaseTag != "" {
		generatedBranchRange = startTag + ".." + releaseTag
	} else {
		generatedBranchRange = startTag + ".." + branchHead
	}

	// Validate generated range
	_, err = RunShell("git rev-parse " + generatedBranchRange)
	if err != nil {
		panic(fmt.Sprintf("Invalid tags/range %v!", generatedBranchRange))
	}

	return generatedBranchRange, nil
}
