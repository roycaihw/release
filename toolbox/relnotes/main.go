// Copyright 2017 Kubernetes Authors
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
	"log"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/google/go-github/github"
	// A copy of istio.io/test-infra/toolbox/util
	// TODO: refactor it since we aren't using a lot of the features
	u "k8s.io/release/toolbox/util"
)

const (
	releaseNoteSuffix = ".releasenote"
)

var (
	// Program flags
	branch    = flag.String("branch", "", "Specify a branch other than the current one")
	endDate   = flag.String("end_date", "", "End date")
	htmlFile  = flag.String("html-file", "", "Produce a html version of the notes")
	label     = flag.String("label", "release-note", "Release-note label")
	mdFile    = flag.String("markdown-file", "", "Specify an alt file to use to store notes")
	order     = flag.String("order", "desc", "The sort order if sort parameter is provided. One of asc or desc.")
	org       = flag.String("user", "kubernetes", "Github owner or org")
	output    = flag.String("output", "./", "Path to output file")
	repos     = flag.String("repos", "", "Github repos, separate using \",\"")
	sort      = flag.String("sort", "create", "The sort field. Can be comments, created, or updated.")
	startDate = flag.String("start_date", "", "Start date")
	version   = flag.String("version", "", "Release version")
)

func main() {
	flag.Parse()

	// User input branch range [[starttag..]endtag], empty if using the default range
	branchRange := flag.Arg(0)

	progPath := strings.Split(os.Args[0], "/")
	prog := progPath[len(progPath)-1]

	if *branch == "" {
		// TODO: add 2>/dev/null to the shell command to avoid error on empty branch
		gitBranchCommand := "git rev-parse --abbrev-ref HEAD"
		_, err := u.Shell(gitBranchCommand)
		if err != nil {
			log.Printf("Not a git repository!")
			return
		}
		*branch, _ = u.Shell(gitBranchCommand)
		*branch = strings.TrimSpace(*branch)
	}

	// Output file path for temporary generated release note and PR notes
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

	// TODO: there are log init, timestamp begin in relnotes script
	fetchPRFromLog(*branch, branchRange, *org, "kubernetes")

	repoList := strings.Split(*repos, ",")
	for _, repo := range repoList {
		log.Printf("Start fetching release note from %s", repo)
		queries := createQueryString(repo)
		log.Printf("Query: %s", queries)

		g := u.NewGithubClientNoAuth(*org)
		issuesResult, err := g.SearchIssues(queries, "", *sort, *order)
		if err != nil {
			log.Printf("Failed to fetch PR with release note for %s: %s", repo, err)
			continue
		}
		if err = fetchRelaseNoteFromRepo(repo, issuesResult); err != nil {
			log.Printf("Failed to get release note for %s: %s", repo, err)
			continue
		}
	}

}

func fetchPRFromLog(currentBranch string, branchRange string, org string, repo string) ([]string, error) {
	// Determine remote branch head
	gitBranchCommand := "git rev-parse refs/remotes/origin/" + currentBranch
	branchHead, err := u.Shell(gitBranchCommand)
	if err != nil {
		return nil, err
	}

	// Last release
	lastRelease, err := LastRelease(org, repo)
	if err != nil {
		return nil, err
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
	prettyRange := branchRange
	// Default range
	if branchRange == "" {
		prettyRange = lastRelease[currentBranch] + ".." + branchHead
	}
	v, err := regexp.Compile("([v0-9.]*-*(alpha|beta|rc)*.*[0-9]*)..([v0-9.]*-*(alpha|beta|rc)*.*[0-9]*)$")
	if err != nil {
		return nil, err
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
	// TODO: haven't tested tags' behavior yet.
	log.Printf("pretty range: %v", prettyRange)
	log.Printf("start: %v", startTag)
	log.Printf("release: %v", releaseTag)

	// Get slice of PRs by executing git log
	return nil, nil
}

// LastRelease looks up the list of releases on github and puts the last release per branch
// into a branch-indexed dictionary
func LastRelease(owner string, repo string) (map[string]string, error) {
	r := make(map[string]string)
	g := github.NewClient(nil)
	repoRelease, _, err := g.Repositories.ListReleases(context.Background(), owner, repo, &github.ListOptions{Page: 2, PerPage: 10})
	if err != nil {
		return nil, err
	}
	for _, release := range repoRelease {
		// Skip draft releases (TODO: no-auth clienct cannot fetch draft releases)
		if *release.Draft {
			continue
		}
		// Alpha releases only on master branch
		if strings.Contains(*release.Name, "-alpha") && r["master"] == "" {
			r["master"] = *release.Name
		} else {
			v, err := regexp.Compile("v([0-9]+.[0-9]+).([0-9]+(-.+)?)")
			if err != nil {
				return nil, err
			}
			version := v.FindStringSubmatch(*release.Name)
			if version != nil {
				// Lastest vx.x.0 release goes on both master and release branch
				if version[2] == "0" && r["master"] == "" {
					r["master"] = version[0]
				}
				branchName := "release-" + version[1]
				if r[branchName] == "" {
					r[branchName] = version[0]
				}

			}
		}
	}
	return r, nil
}

func fetchRelaseNoteFromRepo(repo string, issuesResult *github.IssuesSearchResult) error {
	fileName := filepath.Join(*output, repo+releaseNoteSuffix)
	f, err := os.OpenFile(fileName, os.O_WRONLY|os.O_TRUNC|os.O_CREATE, 0600)
	if err != nil {
		log.Printf("Failed to create output file %s", fileName)
		return err
	}
	defer func() {
		if err := f.Close(); err != nil {
			log.Printf("Error during closing file %s: %s\n", fileName, err)
		}
	}()

	f.WriteString(fmt.Sprintf("%s: %s -- %s release note\n", *org, repo, *version))
	f.WriteString(fmt.Sprintf("Date: %s -- %s\n", *startDate, *endDate))
	f.WriteString(fmt.Sprintf("Total: %d\n", *issuesResult.Total))
	for _, i := range issuesResult.Issues {
		note := fetchReleaseNoteFromPR(i)
		f.WriteString(note)
	}
	if *issuesResult.IncompleteResults {
		f.WriteString("!!Warning: Some release notes missing due to incomplete search result from github.")
	}
	return nil
}

func fetchReleaseNoteFromPR(i github.Issue) (note string) {
	reg := regexp.MustCompile("```release-note((?s).*)```")
	m := reg.FindStringSubmatch(*i.Body)
	if len(m) == 2 {
		note = m[1]
	}
	return note
}

func createQueryString(repo string) []string {
	var queries []string

	queries = addQuery(queries, "repo", *org, "/", repo)
	queries = addQuery(queries, "label", *label)
	queries = addQuery(queries, "is", "merged")
	queries = addQuery(queries, "type", "pr")
	queries = addQuery(queries, "merged", ">", *startDate)
	queries = addQuery(queries, "merged", "<", *endDate)

	return queries
}

func addQuery(queries []string, queryParts ...string) []string {
	if len(queryParts) < 2 {
		log.Printf("Not enough to form a query: %v", queryParts)
		return queries
	}
	for _, part := range queryParts {
		if part == "" {
			return queries
		}
	}

	return append(queries, fmt.Sprintf("%s:%s", queryParts[0], strings.Join(queryParts[1:], "")))
}
