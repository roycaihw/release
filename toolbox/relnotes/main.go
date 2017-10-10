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
	"bytes"
	"context"
	"flag"
	"fmt"
	"io"
	"io/ioutil"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/google/go-github/github"
	u "k8s.io/release/toolbox/util"
)

const (
	k8sReleaseURLPrefix = "https://dl.k8s.io"
)

var (
	// Flags
	// TODO: golang flags and parameters syntex
	branch           = flag.String("branch", "", "Specify a branch other than the current one")
	documentURL      = flag.String("doc-url", "https://docs.k8s.io", "Documentation URL displayed in release notes")
	exampleURLPrefix = flag.String("example-url-prefix", "https://releases.k8s.io/", "Example URL prefix displayed in release notes")
	full             = flag.Bool("full", false, "Force 'full' release format to show all sections of release notes. "+
		"(This is the *default* for new branch X.Y.0 notes)")
	githubToken   = flag.String("github-token", "", "Must be specified, or set the GITHUB_TOKEN environment variable")
	htmlFileName  = flag.String("html-file", "", "Produce a html version of the notes")
	htmlizeMD     = flag.Bool("htmlize-md", false, "Output markdown with html for PRs and contributors (for use in CHANGELOG.md)")
	mdFileName    = flag.String("markdown-file", "", "Specify an alt file to use to store notes")
	owner         = flag.String("owner", "kubernetes", "Github owner or organization")
	preview       = flag.Bool("preview", false, "Report additional branch statistics (used for reporting outside of releases)")
	quiet         = flag.Bool("quiet", false, "Don't display the notes when done")
	releaseBucket = flag.String("release-bucket", "kubernetes-release", "Specify gs bucket to point to in generated notes (informational only)")
	releaseTars   = flag.String("release-tars", "", "Directory of tars to sha256 sum for display")
	repo          = flag.String("repo", "kubernetes", "Github repository")

	// Global
	branchHead = ""
)

func main() {
	// Initialization
	flag.Parse()
	branchRange := flag.Arg(0)
	startingTime := time.Now().Round(time.Second)

	log.Printf("Boolean flags: full: %v, htmlize-md: %v, preview: %v, quiet: %v", *full, *htmlizeMD, *preview, *quiet)
	log.Printf("Input branch range: %s", branchRange)

	// If branch isn't specified in flag, use current branch
	if *branch == "" {
		var err error
		*branch, err = u.GetCurrentBranch()
		if err != nil {
			log.Printf("not a git repository: %s", err)
			os.Exit(1)
		}
	}

	prFileName := fmt.Sprintf("/tmp/release-notes-%s-prnotes", *branch)
	if *mdFileName == "" {
		*mdFileName = fmt.Sprintf("/tmp/release-notes-%s.md", *branch)
	}
	log.Printf("Output markdown file path: %s", *mdFileName)
	if *htmlFileName != "" {
		log.Printf("Output HTML file path: %s", *htmlFileName)
	}

	// If githubToken isn't specified in flag, use the GITHUB_TOKEN environment variable
	if *githubToken == "" {
		*githubToken = os.Getenv("GITHUB_TOKEN")
	}
	client := u.NewClient(*githubToken)

	log.Printf("Gathering release commits from Github...")
	// Get release related commits on the release branch within release range
	releaseCommits, startTag, releaseTag, err := getReleaseCommits(client, *owner, *repo, *branch, branchRange)
	if err != nil {
		log.Printf("failed to get release commits for %s: %s", branchRange, err)
		os.Exit(1)
	}

	// Parse release related PR ids from the release commits
	commitPRs, err := parsePRFromCommit(releaseCommits)
	if err != nil {
		log.Printf("failed to parse release commits: %s", err)
		os.Exit(1)
	}

	// Get number-issue mapping for issues in the repository
	issues, err := u.ListAllIssues(client, *owner, *repo)
	if err != nil {
		log.Printf("failed to list all issues from %s: %s", *repo, err)
		os.Exit(1)
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

	// Generating release note...
	log.Printf("Generating release notes...")

	prFile, err := os.Create(prFileName)
	if err != nil {
		log.Printf("failed to create release note file %s: %s", prFileName, err)
		os.Exit(1)
	}

	// Bootstrap notes for minor (new branch) releases
	if *full || u.IsVer(releaseTag, "dotzero") {
		draftURL := fmt.Sprintf("%s%s/features/master/%s/release-notes-draft.md", u.GithubRawURL, *owner, *branch)
		changelogURL := fmt.Sprintf("%s%s/%s/master/CHANGELOG.md", u.GithubRawURL, *owner, *repo)
		minorRelease(prFile, releaseTag, draftURL, changelogURL)
	} else {
		patchRelease(prFile, startTag, releasePRs, issueMap)
	}

	prFile.Close()

	// Start generating markdown file
	log.Printf("Preparing layout...")

	mdFile, err := os.Create(*mdFileName)
	if err != nil {
		log.Printf("failed to create release note markdown file %s: %s", *mdFileName, err)
		os.Exit(1)
	}

	// Create markdown file body with documentation and example URLs from program flags
	exampleURL := fmt.Sprintf("%s%s/examples", *exampleURLPrefix, *branch)
	createBody(client, mdFile, releaseTag, *branch, *documentURL, exampleURL, *releaseTars)

	// Copy (append) the pull request notes into the output markdown file
	prFile, _ = os.Open(prFileName)
	_, err = io.Copy(mdFile, prFile)
	if err != nil {
		log.Printf("failed to copy from PR file to release note markdown file: %s", err)
	}
	err = mdFile.Sync()
	if err != nil {
		log.Printf("failed to copy from PR file to release note markdown file: %s", err)
	}

	prFile.Close()

	if *preview {
		// If in preview mode, get the pending PRs
		err = getPendingPRs(client, mdFile, *owner, *repo, *branch)
		if err != nil {
			log.Printf("failed to get pending PRs: %s", err)
			os.Exit(1)
		}
	}
	mdFile.Close()

	if *htmlizeMD {
		// Make users and PRs linkable
		// Also, expand anchors (needed for email announce())
		projectGithubURL := fmt.Sprintf("https://github.com/%s/%s", *owner, *repo)
		_, err = u.Shell("sed", "-i", "-e", "s,#\\([0-9]\\{5\\,\\}\\),[#\\1]("+projectGithubURL+"/pull/\\1),g",
			"-e", "s,\\(#v[0-9]\\{3\\}-\\),"+projectGithubURL+"/blob/master/CHANGELOG.md\\1,g",
			"-e", "s,@\\([a-zA-Z0-9-]*\\),[@\\1](https://github.com/\\1),g", *mdFileName)

		if err != nil {
			log.Printf("failed to htmlize markdown file: %s", err)
			os.Exit(1)
		}
	}

	if *preview && *owner == "kubernetes" && *repo == "kubernetes" {
		// If in preview mode, get the current CI job status
		// We do this after htmlizing because we don't want to update the
		// issues in the block of this section
		//
		// NOTE: this function is Kubernetes-specified and runs the find_green_build script under
		// kubernetes/release. Make sure you have the dependencies installed for find_green_build
		// before running this function.
		err = getCIJobStatus(*mdFileName, *branch, *htmlizeMD)
		if err != nil {
			log.Printf("failed to get CI status: %s", err)
			os.Exit(1)
		}
	}

	if *htmlFileName != "" {
		err = createHTMLNote(*htmlFileName, *mdFileName)
		if err != nil {
			log.Printf("failed to generate HTML release note: %s", err)
		}
	}

	if !*quiet {
		log.Printf("Displaying the markdown release note to stdout...")
		dat, err := ioutil.ReadFile(*mdFileName)
		if err != nil {
			log.Printf("failed to read markdown release note: %s", err)
		}
		fmt.Print(string(dat))
	}

	log.Printf("Successfully generated release note. Total running time: %s", time.Now().Round(time.Second).Sub(startingTime).String())

	return
}

// getPendingPRs gets pending PRs on given branch in the repo.
func getPendingPRs(c *github.Client, f *os.File, owner, repo, branch string) error {
	log.Printf("Getting pending PR status...")
	f.WriteString("-------\n")
	f.WriteString(fmt.Sprintf("## PENDING PRs on the %s branch\n", branch))

	if *htmlizeMD {
		f.WriteString("PR | Milestone | User | Date | Commit Message\n")
		f.WriteString("-- | --------- | ---- | ---- | --------------\n")
	}

	var query []string
	query = u.AddQuery(query, "repo", owner, "/", repo)
	query = u.AddQuery(query, "is", "open")
	query = u.AddQuery(query, "type", "pr")
	query = u.AddQuery(query, "base", branch)
	pendingPRs, err := u.SearchIssues(c, strings.Join(query, " "))
	if err != nil {
		return fmt.Errorf("failed to search pending PRs: %s", err)
	}

	for _, pr := range pendingPRs {
		var str string
		// escape '*' in commit messages so they don't mess up formatting
		msg := strings.Replace(*pr.Title, "*", "", -1)
		milestone := "null"
		if pr.Milestone != nil {
			milestone = *pr.Milestone.Title
		}
		if *htmlizeMD {
			str = fmt.Sprintf("#%-8d | %-4s | @%-10s| %s   | %s\n", *pr.Number, milestone, *pr.User.Login, pr.UpdatedAt.Format("Mon Jan  2 15:04:05 MST 2006"), msg)
		} else {
			str = fmt.Sprintf("#%-8d  %-4s  @%-10s %s    %s\n", *pr.Number, milestone, *pr.User.Login, pr.UpdatedAt.Format("Mon Jan  2 15:04:05 MST 2006"), msg)
		}
		f.WriteString(str)
	}
	f.WriteString("\n\n")
	return nil
}

// createHTMLNote generates HTML release note based on the input markdown release note.
func createHTMLNote(htmlFileName, mdFileName string) error {
	log.Printf("Generating HTML release note...")
	cssFileName := "/tmp/release_note_cssfile"
	cssFile, err := os.Create(cssFileName)
	if err != nil {
		return fmt.Errorf("failed to create css file %s: %s", cssFileName, err)
	}

	cssFile.WriteString("<style type=text/css> ")
	cssFile.WriteString("table,th,tr,td {border: 1px solid gray; ")
	cssFile.WriteString("border-collapse: collapse;padding: 5px;} ")
	cssFile.WriteString("</style>")
	cssFile.Close()

	htmlStr, err := u.Shell("pandoc", "-H", cssFileName, "--from", "markdown_github", "--to", "html", mdFileName)
	if err != nil {
		return fmt.Errorf("failed to generate html content: %s", err)
	}

	htmlFile, err := os.Create(htmlFileName)
	if err != nil {
		return fmt.Errorf("failed to create html file: %s", err)
	}
	defer htmlFile.Close()

	htmlFile.WriteString(htmlStr)
	return nil
}

// getCIJobStatus runs the script find_green_build and append CI job status to outputFile.
// NOTE: this function is Kubernetes-specified and runs the find_green_build script under
// kubernetes/release. Make sure you have the dependencies installed for find_green_build
// before running this function.
func getCIJobStatus(outputFile, branch string, htmlize bool) error {
	log.Printf("Getting CI job status (this may take a while)...")

	red := "<span style=\"color:red\">"
	green := "<span style=\"color:green\">"
	off := "</span>"

	if htmlize {
		red = "<FONT COLOR=RED>"
		green = "<FONT COLOR=GREEN>"
		off = "</FONT>"
	}

	var extraFlag string

	if strings.Contains(branch, "release-") {
		// If working on a release branch assume --official for the purpose of displaying
		// find_green_build output
		extraFlag = "--official"
	} else {
		// For master branch, limit the analysis to 30 primary ci jobs. This is necessary
		// due to the recently expanded blocking test list for master. The expanded test
		// list is often unable to find a complete passing set and find_green_build runs
		// unbounded for hours
		extraFlag = "--limit=30"
	}

	f, err := os.OpenFile(outputFile, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
	if err != nil {
		return err
	}

	defer f.Close()

	f.WriteString(fmt.Sprintf("## State of %s branch\n", branch))

	// Call script find_green_build to get CI job status
	content, err := u.Shell(os.Getenv("GOPATH")+"/src/k8s.io/release/find_green_build", "-v", extraFlag, branch)
	if err == nil {
		f.WriteString(fmt.Sprintf("%sGOOD TO GO!%s\n\n", green, off))
	} else {
		f.WriteString(fmt.Sprintf("%sNOT READY%s\n\n", red, off))
	}

	f.WriteString("### Details\n```\n")
	f.WriteString(content)
	f.WriteString("```\n")

	log.Printf("CI job status fetched.")
	return nil
}

// createBody creates the general documentation, example and downloads table body for the
// markdown file.
func createBody(c *github.Client, f *os.File, releaseTag, branch, docURL, exampleURL, releaseTars string) {
	var title string
	if *preview {
		title = "Branch "
	}

	if releaseTag == "HEAD" || releaseTag == branchHead {
		title += branch
	} else {
		title += releaseTag
	}

	if *preview {
		f.WriteString(fmt.Sprintf("**Release Note Preview - generated on %s**\n", time.Now().Format("Mon Jan  2 15:04:05 MST 2006")))
	}

	f.WriteString(fmt.Sprintf("\n# %s\n\n", title))
	f.WriteString(fmt.Sprintf("[Documentation](%s) & [Examples](%s)\n\n", docURL, exampleURL))

	if releaseTars != "" {
		f.WriteString(fmt.Sprintf("## Downloads for %s\n\n", title))
		createDownloadsTable(f, releaseTag, "", releaseTars+"/kubernetes.tar.gz", releaseTars+"/kubernetes-src.tar.gz")
		createDownloadsTable(f, releaseTag, "Client Binaries", releaseTars+"/kubernetes-client*.tar.gz")
		createDownloadsTable(f, releaseTag, "Server Binaries", releaseTars+"/kubernetes-server*.tar.gz")
		createDownloadsTable(f, releaseTag, "Node Binaries", releaseTars+"/kubernetes-node*.tar.gz")
		f.WriteString("\n")
	}
}

// createDownloadTable creates table of download link and sha256 hash for given file.
func createDownloadsTable(f *os.File, releaseTag, heading string, filename ...string) {
	var urlPrefix string

	if *releaseBucket == "kubernetes-release" {
		urlPrefix = k8sReleaseURLPrefix
	} else {
		urlPrefix = fmt.Sprintf("https://storage.googleapis.com/%s/release", *releaseBucket)
	}

	if heading != "" {
		f.WriteString(fmt.Sprintf("\n### %s\n", heading))
	}

	f.WriteString("\n")
	f.WriteString("filename | sha256 hash\n")
	f.WriteString("-------- | -----------\n")

	files := make([]string, 0)
	for _, name := range filename {
		fs, _ := filepath.Glob(name)
		for _, v := range fs {
			files = append(files, v)
		}
	}

	for _, file := range files {
		fn := filepath.Base(file)
		sha, err := u.GetSha256(file)
		if err != nil {
			log.Printf("failed to calc SHA256 of file %s: %s", file, err)
			os.Exit(1)
		}
		f.WriteString(fmt.Sprintf("[%s](%s/%s/%s) | `%s`\n", fn, urlPrefix, releaseTag, fn, sha))
	}
}

// minorReleases performs a minor (vX.Y.0) release by fetching the release template and aggregate
// previous release in series.
func minorRelease(f *os.File, release, draftURL, changelogURL string) {

	// Check for draft and use it if available
	log.Printf("Checking if draft release notes exist for %s...", release)

	resp, err := http.Get(draftURL)
	if err == nil {
		defer resp.Body.Close()
	}

	// TODO: find a better way to tell failed response
	if err == nil && (resp.StatusCode == 200 || resp.StatusCode == 304) {
		log.Printf("Draft found - using for release notes...")
		_, err = io.Copy(f, resp.Body)
		if err != nil {
			log.Printf("error during copy to file: %s", err)
			return
		}
		f.WriteString("\n")
	} else {
		log.Printf("No draft found - creating generic template...")
		f.WriteString("## Major Themes\n\n* TBD\n\n## Other notable improvements\n\n* TBD\n\n## Known Issues\n\n* TBD\n\n## Provider-specific Notes\n\n* TBD\n\n")
	}

	// Aggregate all previous release in series
	f.WriteString(fmt.Sprintf("### Previous Release Included in %s\n\n", release))

	// Regexp Example:
	// Assume the release tag is v1.7.0, this regexp matches "- [v1.7.0-" in
	//     "- [v1.7.0-rc.1](#v170-rc1)"
	//     "- [v1.7.0-beta.2](#v170-beta2)"
	reAnchor, _ := regexp.Compile(fmt.Sprintf("- \\[%s-", release))

	resp, err = http.Get(changelogURL)
	if err == nil {
		defer resp.Body.Close()
	}

	// TODO: find a better way to tell failed response
	if err == nil && (resp.StatusCode == 200 || resp.StatusCode == 304) {
		buf := new(bytes.Buffer)
		buf.ReadFrom(resp.Body)
		for _, line := range strings.Split(buf.String(), "\n") {
			if anchor := reAnchor.FindStringSubmatch(line); anchor != nil {
				f.WriteString(line + "\n")
			}
		}
		f.WriteString("\n")
	}

}

// patchRelease performs a patch (vX.Y.Z) release by printing out all the related changes.
func patchRelease(f *os.File, start string, prs []int, issueMap map[int]*github.Issue) {
	// Release note for different labels (TODO: "release-note" label for now since "experimental" and
	// "action" are deprecated)
	f.WriteString(fmt.Sprintf("## Changelog since %s\n\n", start))

	if len(prs) > 0 {
		f.WriteString("### Other notable changes\n\n")
		for _, pr := range prs {
			f.WriteString(fmt.Sprintf("* %s (#%d, @%s)\n", extractReleaseNote(issueMap[pr]), pr, *issueMap[pr].User.Login))
		}
		f.WriteString("\n")
	} else {
		f.WriteString("**No notable changes for this release**\n\n")
	}
}

// extractReleaseNote tries to fetch release note from PR body, otherwise uses PR title.
func extractReleaseNote(pr *github.Issue) string {
	// Regexp Example:
	// This regexp matches the release note section in Kubernetes pull request template:
	// https://github.com/kubernetes/kubernetes/blob/master/.github/PULL_REQUEST_TEMPLATE.md
	re, _ := regexp.Compile("```release-note\r\n(.+)\r\n```")
	if note := re.FindStringSubmatch(*pr.Body); note != nil {
		return note[1]
	}
	return *pr.Title
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
	branchHead = *b.Commit.SHA

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

	// Regexp Example:
	// This regexp matches the Git branch range in the format of [[startTag..]endTag]. For example:
	//
	//     ""
	//     "v1.1.4.."
	//     "v1.1.4..v1.1.7"
	//     "v1.1.7"
	re, _ := regexp.Compile("([v0-9.]*-*(alpha|beta|rc)*\\.*[0-9]*)\\.\\.([v0-9.]*-*(alpha|beta|rc)*\\.*[0-9]*)$")
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

	// Get commits for specified branch and range
	tStart, ok := u.GetCommitDate(c, owner, repo, startTag, tags)
	if ok != true {
		return nil, "", "", fmt.Errorf("failed to get start commit date: %s", startTag)
	}
	tEnd, ok := u.GetCommitDate(c, owner, repo, releaseTag, tags)
	if ok != true {
		return nil, "", "", fmt.Errorf("failed to get release commit date: %s", releaseTag)
	}

	releaseCommits, err := u.ListAllCommits(c, owner, repo, branch, tStart, tEnd)
	if err != nil {
		return nil, "", "", fmt.Errorf("failed to fetch release repo commits: %s", err)
	}

	return releaseCommits, startTag, releaseTag, nil
}

// parsePRFromCommit goes through commit messages, and parse PR IDs for normal pull requests as
// well as cherry picks.
func parsePRFromCommit(commits []*github.RepositoryCommit) ([]int, error) {
	prs := make([]int, 0)
	prsMap := make(map[int]bool)

	// Regexp example:
	// This regexp matches (Note that it supports multiple-source cherry pick)
	//
	// "automated-cherry-pick-of-#12345-#23412-"
	// "automated-cherry-pick-of-#23791-"
	reCherry, _ := regexp.Compile("automated-cherry-pick-of-(#[0-9]+-){1,}")
	reCherryID, _ := regexp.Compile("#([0-9]+)-")
	reMerge, _ := regexp.Compile("^Merge pull request #([0-9]+) from")

	for _, c := range commits {
		// Deref all PRs back to master
		// Match cherry pick PRs first and then normal pull requests
		// Paying special attention to automated cherrypicks that could have multiple
		// sources
		if cpStr := reCherry.FindStringSubmatch(*c.Commit.Message); cpStr != nil {
			cpPRs := reCherryID.FindAllStringSubmatch(cpStr[0], -1)
			for _, pr := range cpPRs {
				id, err := strconv.Atoi(pr[1])
				if err != nil {
					return nil, err
				}
				if prsMap[id] == false {
					prs = append(prs, id)
					prsMap[id] = true
				}
			}
		} else if pr := reMerge.FindStringSubmatch(*c.Commit.Message); pr != nil {
			id, err := strconv.Atoi(pr[1])
			if err != nil {
				return nil, err
			}
			if prsMap[id] == false {
				prs = append(prs, id)
				prsMap[id] = true
			}
		}
	}

	return prs, nil
}
