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
	"context"
	"flag"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/google/go-github/github"
	u "k8s.io/release/toolbox/util"
)

var (
	// TODO: golang flags and parameters syntex
	branch = flag.String("branch", "", "Specify a branch other than the current one")
	full   = flag.Bool("full", false, "Force 'full' release format to show all sections of release notes. "+
		"(This is the *default* for new branch X.Y.0 notes)")
	githubToken = flag.String("github-token", "", "Must be specified, or set the GITHUB_TOKEN environment variable")
	htmlFile    = flag.String("html-file", "", "Produce a html version of the notes")
	mdFile      = flag.String("markdown-file", "", "Specify an alt file to use to store notes")
	owner       = flag.String("owner", "kubernetes", "Github owner or organization")
	repo        = flag.String("repo", "kubernetes", "Github repository")
)

func main() {
	// Initialization
	flag.Parse()
	branchRange := flag.Arg(0)

	// If branch isn't specified in flag, use current branch
	if *branch == "" {
		var err error
		*branch, err = u.GetCurrentBranch()
		if err != nil {
			log.Printf("not a git repository: %s", err)
			return
		}
	}

	// If githubToken isn't specified in flag, use the GITHUB_TOKEN environment variable
	if *githubToken == "" {
		*githubToken = os.Getenv("GITHUB_TOKEN")
	}
	client := u.NewClient(*githubToken)

	// Get release related commits on the release branch within release range
	releaseCommits, startTag, releaseTag, err := getReleaseCommits(client, *owner, *repo, *branch, branchRange)
	if err != nil {
		log.Printf("failed to get release commits for %s: %s", branchRange, err)
		return
	}

	// Parse release related PR ids from the release commits
	commitPRs, err := parsePRFromCommit(releaseCommits)
	if err != nil {
		log.Printf("failed to parse release commits: %s", err)
		return
	}

	// Get number-issue mapping for issues in the repository
	issues, err := u.ListAllIssues(client, *owner, *repo)
	if err != nil {
		log.Printf("failed to list all issues from %s: %s", *repo, err)
		return
	}
	issueMap := make(map[int]*github.Issue)
	for _, i := range issues {
		issueMap[*i.Number] = i
	}

	// Get release note PRs by examining release-note label on commit PRs
	releasePRs := make([]int, 0)
	for _, pr := range commitPRs {
		if u.HasLabel(issueMap[pr], "release-note") {
			releasePRs = append(releasePRs, pr)
		}
	}

	// Generate release note
	prNotes := "./testfile.md"
	log.Printf("Generating release notes...")
	prNotesFile, err := os.Create(prNotes)
	if err != nil {
		log.Printf("failed to create release note file: %s", err)
		return
	}
	defer prNotesFile.Close()

	// Bootstrap notes for major (new branch) releases
	if *full || u.IsVer(releaseTag, "dotzero") {
		// Check for draft and use it if available
		log.Printf("Checking if draft release notes exist for %v...", releaseTag)
		resp, err := http.Get(u.GithubRawURL + *owner + "/features/master/" + *branch + "/release-notes-draft.md")
		// TODO: find a better way to tell error response
		if err == nil && (resp.StatusCode == 200 || resp.StatusCode == 304) {
			defer resp.Body.Close()
			log.Printf("Draft found - using for release notes...")
			_, err = io.Copy(prNotesFile, resp.Body)
			if err != nil {
				log.Printf("error during copy file: %s", err)
				return
			}
		} else {
			log.Printf("No draft found - creating generic template...")
			prNotesFile.WriteString("## Major Themes\n\n* TBD\n\n## Other notable improvements\n\n" +
				"* TBD\n\n## Known Issues\n\n* TBD\n\n## Provider-specific Notes\n\n* TBD\n\n")
		}
	}

	// Aggregate all previous release in series
	prNotesFile.WriteString("### Previous Release Included in " + releaseTag + "\n")
	prNotesFile.WriteString("## Changelog since " + startTag + "\n")

	// Release note for different labels. TODO: release-note for now
	prNotesFile.WriteString("### Other notable changes\n")
	// for _, issue := range issues {
	// 	prNotesFile.WriteString(fmt.Sprintf("    %s\n", *(issue.Title)))
	// }

	return
}

// determineRange examines a Git branch range in the format of [[startTag..]endTag], and
// determines a valid range. For example:
//
//     ""                       - last release to HEAD on the branch
//     "v1.1.4.."               - v1.1.4 to HEAD
//     "v1.1.4..v1.1.7"         - v1.1.4 to v1.1.7
//     "v1.1.7"                 - last release on the branch to v1.1.7
//
// NOTE: the input branch must be the corresponding release branch w.r.t. input range. For example:
//
//     Getting "v1.1.4..v1.1.7" on branch "release-1.1" makes sense
//     Getting "v1.1.4..v1.1.7" on branch "release-1.2" doesn't
func determineRange(c *github.Client, owner, repo, branch, branchRange string) (startTag, releaseTag string, err error) {
	b, _, err := c.Repositories.GetBranch(context.Background(), owner, repo, branch)
	if err != nil {
		return "", "", err
	}
	branchHead := *b.Commit.SHA

	lastRelease, err := u.LastReleases(c, owner, repo)
	if err != nil {
		return "", "", err
	}

	// If lastRelease[branch] is unset, attempt to get the last release from the parent branch
	// and then master
	if i := strings.LastIndex(branch, "."); lastRelease[branch] == "" && i != -1 {
		lastRelease[branch] = lastRelease[branch[:i]]
	}
	if lastRelease[branch] == "" {
		lastRelease[branch] = lastRelease["master"]
	}

	re, err := regexp.Compile("([v0-9.]*-*(alpha|beta|rc)*\\.*[0-9]*)\\.\\.([v0-9.]*-*(alpha|beta|rc)*\\.*[0-9]*)$")
	if err != nil {
		return "", "", err
	}
	tags := re.FindStringSubmatch(branchRange)
	if tags != nil {
		startTag = tags[1]
		releaseTag = tags[3]
	} else {
		startTag = lastRelease[branch]
		releaseTag = branchHead
	}

	if startTag == "" {
		return "", "", fmt.Errorf("unable to set beginning of range automatically")
	}
	if releaseTag == "" {
		releaseTag = branchHead
	}

	return startTag, releaseTag, nil
}

// getReleaseCommits given a Git branch range in the format of [[startTag..]endTag], determines
// a valid range and returns all the commits on the branch in that range.
func getReleaseCommits(c *github.Client, owner, repo, branch, branchRange string) ([]*github.RepositoryCommit, string, string, error) {
	// Get start and release tag/commit based on input branch range
	startTag, releaseTag, err := determineRange(c, owner, repo, branch, branchRange)
	if err != nil {
		return nil, "", "", fmt.Errorf("failed to determine branch range: %s", err)
	}

	// Get all tags in the repository
	tags, err := u.ListAllTags(c, owner, repo)
	if err != nil {
		return nil, "", "", fmt.Errorf("failed to fetch repo tags: %s", err)
	}

	// Get all commits on the branch
	commits, err := u.ListAllCommits(c, owner, repo, branch, time.Time{}, time.Time{})
	if err != nil {
		return nil, "", "", fmt.Errorf("failed to fetch all repo commits: %s", err)
	}

	// Get commits for specified branch and range
	tStart, ok := u.GetCommitDate(startTag, tags, commits)
	if ok != true {
		return nil, "", "", fmt.Errorf("failed to get start commit date: %s", startTag)
	}
	tEnd, ok := u.GetCommitDate(releaseTag, tags, commits)
	if ok != true {
		return nil, "", "", fmt.Errorf("failed to get release commit date: %s", releaseTag)
	}

	releaseCommits, err := u.ListAllCommits(c, owner, repo, branch, tStart, tEnd)
	if err != nil {
		return nil, "", "", fmt.Errorf("failed to fetch release repo commits: %s", err)
	}

	return releaseCommits, startTag, releaseTag, nil
}

func parsePRFromCommit(commits []*github.RepositoryCommit) ([]int, error) {
	prs := make([]int, 0)

	reCherry, err := regexp.Compile("automated-cherry-pick-of-#([0-9]+)-{1,}")
	if err != nil {
		return nil, err
	}
	reMerge, err := regexp.Compile("Merge pull request #([0-9]+) from")
	if err != nil {
		return nil, err
	}

	for _, c := range commits {
		if pr := reCherry.FindStringSubmatch(*c.Commit.Message); pr != nil {
			id, err := strconv.Atoi(pr[1])
			if err != nil {
				return nil, err
			}
			prs = append(prs, id)
		} else if pr := reMerge.FindStringSubmatch(*c.Commit.Message); pr != nil {
			id, err := strconv.Atoi(pr[1])
			if err != nil {
				return nil, err
			}
			prs = append(prs, id)
		}
	}

	return prs, nil
}
