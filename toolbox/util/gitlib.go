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
	"regexp"
	"strings"
)

// GetCurrentBranch gets the branch name where the program is called.
func GetCurrentBranch() (string, error) {
	branch, err := Shell("git", "rev-parse", "--abbrev-ref", "HEAD")
	if err != nil {
		return "", err
	}

	// Remove trailing newline
	branch = strings.TrimSpace(branch)
	return branch, nil
}

// IsVer checks if input version number matches input version type among: "release", "dotzero" and
// "build". The function returns true if the version number matches the version type; returns false
// otherwise.
func IsVer(version string, t string) bool {
	m := make(map[string]string)
	m["release"] = "v(0|[1-9][0-9]*)\\.(0|[1-9][0-9]*)\\.(0|[1-9][0-9]*)(-[a-zA-Z0-9]+)*\\.*(0|[1-9][0-9]*)?"
	m["dotzero"] = "v(0|[1-9][0-9]*)\\.(0|[1-9][0-9]*)\\.0$"
	m["build"] = "([0-9]{1,})\\+([0-9a-f]{5,40})"

	re, _ := regexp.Compile(m[t])
	return re.MatchString(version)
}
