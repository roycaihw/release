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
	"fmt"
	"log"
	"regexp"
	"strings"
	"time"

	"golang.org/x/oauth2"

	"github.com/google/go-github/github"
)

const (
	// GithubRawURL is the url prefix for getting raw github user content.
	GithubRawURL = "https://raw.githubusercontent.com/"
)

// NewClient sets up a new github client with input assess token.
func NewClient(githubToken string) *github.Client {
	ctx := context.Background()
	ts := oauth2.StaticTokenSource(
		&oauth2.Token{AccessToken: githubToken},
	)
	tc := oauth2.NewClient(ctx, ts)

	return github.NewClient(tc)
}

// LastReleases looks up the list of releases on github and puts the last release per branch
// into a branch-indexed dictionary.
func LastReleases(c *github.Client, owner, repo string) (map[string]string, error) {
	lastRelease := make(map[string]string)

	r, err := ListAllReleases(c, owner, repo)
	if err != nil {
		return nil, err
	}

	for _, release := range r {
		// Skip draft releases
		if *release.Draft {
			continue
		}
		// Alpha release goes only on master branch
		if strings.Contains(*release.TagName, "-alpha") && lastRelease["master"] == "" {
			lastRelease["master"] = *release.TagName
		} else {
			re, _ := regexp.Compile("v([0-9]+\\.[0-9]+)\\.([0-9]+(-.+)?)")
			version := re.FindStringSubmatch(*release.TagName)

			if version != nil {
				// Lastest vx.y.0 release goes on both master and release-vx.y branch
				if version[2] == "0" && lastRelease["master"] == "" {
					lastRelease["master"] = *release.TagName
				}

				branchName := "release-" + version[1]
				if lastRelease[branchName] == "" {
					lastRelease[branchName] = *release.TagName
				}
			}
		}
	}

	return lastRelease, nil
}

// ListAllReleases lists all releases for given owner and repo.
func ListAllReleases(c *github.Client, owner, repo string) ([]*github.RepositoryRelease, error) {
	lo := &github.ListOptions{
		Page:    1,
		PerPage: 100,
	}

	releases, resp, err := c.Repositories.ListReleases(context.Background(), owner, repo, lo)
	if err != nil {
		return nil, err
	}
	lo.Page++

	for lo.Page <= resp.LastPage {
		re, _, err := c.Repositories.ListReleases(context.Background(), owner, repo, lo)
		if err != nil {
			return nil, err
		}
		for _, r := range re {
			releases = append(releases, r)
		}
		lo.Page++
	}
	return releases, nil
}

// ListAllIssues lists all issues and PRs for given owner and repo.
func ListAllIssues(c *github.Client, owner, repo string) ([]*github.Issue, error) {
	lo := &github.ListOptions{
		Page:    1,
		PerPage: 100,
	}
	ilo := &github.IssueListByRepoOptions{
		State:       "all",
		ListOptions: *lo,
	}

	issues, resp, err := c.Issues.ListByRepo(context.Background(), owner, repo, ilo)
	if err != nil {
		return nil, err
	}
	ilo.ListOptions.Page++

	for ilo.ListOptions.Page <= resp.LastPage {
		is, _, err := c.Issues.ListByRepo(context.Background(), owner, repo, ilo)
		if err != nil {
			return nil, err
		}
		for _, i := range is {
			issues = append(issues, i)
		}
		ilo.ListOptions.Page++
	}
	return issues, nil
}

// ListAllTags lists all tags for given owner and repo.
func ListAllTags(c *github.Client, owner, repo string) ([]*github.RepositoryTag, error) {
	lo := &github.ListOptions{
		Page:    1,
		PerPage: 100,
	}

	tags, resp, err := c.Repositories.ListTags(context.Background(), owner, repo, lo)
	if err != nil {
		return nil, err
	}
	lo.Page++

	for lo.Page <= resp.LastPage {
		ta, _, err := c.Repositories.ListTags(context.Background(), owner, repo, lo)
		if err != nil {
			return nil, err
		}
		for _, t := range ta {
			tags = append(tags, t)
		}
		lo.Page++
	}
	return tags, nil
}

// ListAllCommits lists all commits for given owner, repo, branch and time range.
func ListAllCommits(c *github.Client, owner, repo, branch string, start, end time.Time) ([]*github.RepositoryCommit, error) {
	lo := &github.ListOptions{
		Page:    1,
		PerPage: 100,
	}

	clo := &github.CommitsListOptions{
		SHA:         branch,
		Since:       start,
		Until:       end,
		ListOptions: *lo,
	}

	commits, resp, err := c.Repositories.ListCommits(context.Background(), owner, repo, clo)
	if err != nil {
		return nil, err
	}
	clo.ListOptions.Page++

	for clo.ListOptions.Page <= resp.LastPage {
		co, _, err := c.Repositories.ListCommits(context.Background(), owner, repo, clo)
		if err != nil {
			return nil, err
		}
		for _, commit := range co {
			commits = append(commits, commit)
		}
		clo.ListOptions.Page++
	}
	return commits, nil
}

// GetCommitDate gets commit time for given tag/commit, provided with repository tags and commits.
// The function returns ok as false if input tag/commit cannot be found in the repository.
func GetCommitDate(c *github.Client, owner, repo, tagCommit string, tags []*github.RepositoryTag) (date time.Time, ok bool) {
	// If input string is a tag, convert it into SHA
	for _, t := range tags {
		if tagCommit == *t.Name {
			tagCommit = *t.Commit.SHA
			break
		}
	}
	commit, _, err := c.Git.GetCommit(context.Background(), owner, repo, tagCommit)
	if err != nil {
		return time.Time{}, false
	}
	return *commit.Committer.Date, true
}

// HasLabel checks if input github issue contains input label.
func HasLabel(i *github.Issue, label string) bool {
	for _, l := range i.Labels {
		if *l.Name == label {
			return true
		}
	}

	return false
}

// SearchIssues gets all issues matching search query.
// NOTE: Github Search API has tight rate limit. For large search request, use ListAllIssues instead.
func SearchIssues(c *github.Client, query string) ([]github.Issue, error) {
	lo := &github.ListOptions{
		Page:    1,
		PerPage: 100,
	}

	so := &github.SearchOptions{
		ListOptions: *lo,
	}

	issues := make([]github.Issue, 0)
	result, resp, err := c.Search.Issues(context.Background(), query, so)
	if err != nil {
		return nil, err
	}
	for _, i := range result.Issues {
		issues = append(issues, i)
	}
	so.ListOptions.Page++

	for so.ListOptions.Page <= resp.LastPage {
		result, _, err = c.Search.Issues(context.Background(), query, so)
		if err != nil {
			return nil, err
		}
		for _, i := range result.Issues {
			issues = append(issues, i)
		}
		so.ListOptions.Page++
	}
	return issues, nil
}

// AddQuery forms a Github query by appending new query parts to input query
func AddQuery(query []string, queryParts ...string) []string {
	if len(queryParts) < 2 {
		log.Printf("not enough parts to form a query: %v", queryParts)
		return query
	}
	for _, part := range queryParts {
		if part == "" {
			return query
		}
	}

	return append(query, fmt.Sprintf("%s:%s", queryParts[0], strings.Join(queryParts[1:], "")))
}
