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
	"context"
	"log"
	"regexp"
	"strconv"
	"strings"

	"github.com/google/go-github/github"
	"golang.org/x/oauth2"
)

const (
	// K8SGithubRawOrg is the url prefix for getting raw github user content
	K8SGithubRawOrg = "https://raw.githubusercontent.com/kubernetes"
)

// LastRelease looks up the list of releases on github and puts the last release per branch
// into a branch-indexed dictionary
func LastRelease(owner string, repo string, githubToken string) (map[string]string, error) {
	r := make(map[string]string)

	ctx := context.Background()
	ts := oauth2.StaticTokenSource(
		&oauth2.Token{AccessToken: githubToken},
	)
	tc := oauth2.NewClient(ctx, ts)
	g := github.NewClient(tc)
	// TODO: using default #pages and per page options
	repoRelease, _, err := g.Repositories.ListReleases(context.Background(), owner, repo, &github.ListOptions{})
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
			v, err := regexp.Compile("v([0-9]+\\.[0-9]+)\\.([0-9]+(-.+)?)")
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

// FetchPRByLabel gets PR from specified repo with input label
// NOTE: Github Search API only allows fetching the first 1000 results, therefore the max #PR returned from this method is 1000.
func FetchPRByLabel(label string, org string, repo string, githubToken string, sort string, order string) (map[string]bool, error) {
	m := make(map[string]bool)
	var queries []string

	queries = addQuery(queries, "repo", org, "/", repo)
	queries = addQuery(queries, "label", label)
	queries = addQuery(queries, "is", "merged")
	queries = addQuery(queries, "type", "pr")
	log.Printf("My Query: %s", queries)

	ctx := context.Background()
	ts := oauth2.StaticTokenSource(
		&oauth2.Token{AccessToken: githubToken},
	)
	tc := oauth2.NewClient(ctx, ts)
	g := github.NewClient(tc)

	q := strings.Join(queries, " ")
	numPerPage := 100
	listOption := &github.ListOptions{
		Page:    1,
		PerPage: numPerPage,
	}
	searchOption := &github.SearchOptions{
		Sort:        sort,
		Order:       order,
		ListOptions: *listOption,
	}
	prResult, _, err := g.Search.Issues(context.Background(), q, searchOption)
	if err != nil {
		log.Printf("Failed to fetch PR with release note for %s: %s", repo, err)
		return nil, err
	}
	numPRs := *prResult.Total
	count := 0
	for count*numPerPage < numPRs && count*numPerPage < 1000 {
		listOption.Page = count + 1
		searchOption.ListOptions = *listOption
		prResult, _, err = g.Search.Issues(context.Background(), q, searchOption)
		if err != nil {
			log.Printf("Failed to fetch PR with release note for %s: %s", repo, err)
			return nil, err
		}

		for _, pr := range prResult.Issues {
			m[strconv.Itoa(*pr.Number)] = true
		}
		count++
	}
	log.Printf("Total #prs with release-note label: %v.", numPRs)

	return m, nil
}

// FetchAllIssues gets all the Issues from a specified repo
func FetchAllIssues(owner string, repo string, githubToken string, sort string, order string) ([]*github.Issue, error) {
	ctx := context.Background()
	ts := oauth2.StaticTokenSource(
		&oauth2.Token{AccessToken: githubToken},
	)
	tc := oauth2.NewClient(ctx, ts)
	g := github.NewClient(tc)

	numPerPage := 100
	listOption := &github.ListOptions{
		Page:    1,
		PerPage: numPerPage,
	}
	options := &github.IssueListByRepoOptions{
		Sort:        sort,
		Direction:   order,
		ListOptions: *listOption,
	}
	issueResult, _, err := g.Issues.ListByRepo(context.Background(), owner, repo, options)
	if err != nil {
		log.Printf("Failed to fetch Issues from %s: %s", repo, err)
		return nil, err
	}

	return issueResult, nil
}
