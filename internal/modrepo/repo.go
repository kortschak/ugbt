// Copyright ©2021 Dan Kortschak. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// Copyright 2019 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// Package modrepo provide a function to obtain the repo URL for a module
// path. It is a cut down version of golang.org/x/pkgsite/internal/source.
package modrepo

import (
	"context"
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"net/http"
	"regexp"
	"strings"
)

const (
	goSourceRepoURL = "https://cs.opensource.google/go/go"
	goIssuesURL     = "https://github.com/golang/go/issues"
)

// URL returns the repository corresponding to the module path.
func URL(ctx context.Context, mod string) (repo, bugs string, _ error) {
	// The example.com domain can never be real; it is reserved for testing
	// (https://en.wikipedia.org/wiki/Example.com). Treat it as if it used
	// GitHub templates.
	if strings.HasPrefix(mod, "example.com/") {
		repo = trimVCSSuffix("https://" + mod)
		return repo, repo, nil
	}

	// standard is the name of the module for the standard library.
	const standard = "std"
	if mod == standard {
		return goSourceRepoURL, goIssuesURL, nil
	}

	repo, bugsFor, err := matchStatic(mod)
	if err != nil {
		meta, err := fetchMeta(ctx, mod)
		if err != nil {
			return "", "", err
		}
		repo = strings.TrimSuffix(meta.repoURL, "/")
		_, bugsFor, _ = matchStatic(removeHTTPScheme(meta.repoURL))
	} else {
		repo = trimVCSSuffix("https://" + repo)
	}
	if strings.HasPrefix(mod, "golang.org/") {
		repo, bugs = adjustGoRepoInfo(repo, mod)
		return repo, bugs, nil
	}
	return repo, bugsFor(repo), nil
}

// csNonXRepos is a set of repos hosted at https://cs.opensource.google/go,
// that are not an x/repo.
var csNonXRepos = map[string]bool{
	"dl":        true,
	"proposal":  true,
	"vscode-go": true,
}

// csXRepos is the set of repos hosted at https://cs.opensource.google/go,
// that have a x/ prefix.
//
// x/scratch is not included.
var csXRepos = map[string]bool{
	"x/arch":       true,
	"x/benchmarks": true,
	"x/blog":       true,
	"x/build":      true,
	"x/crypto":     true,
	"x/debug":      true,
	"x/example":    true,
	"x/exp":        true,
	"x/image":      true,
	"x/mobile":     true,
	"x/mod":        true,
	"x/net":        true,
	"x/oauth2":     true,
	"x/perf":       true,
	"x/pkgsite":    true,
	"x/playground": true,
	"x/review":     true,
	"x/sync":       true,
	"x/sys":        true,
	"x/talks":      true,
	"x/term":       true,
	"x/text":       true,
	"x/time":       true,
	"x/tools":      true,
	"x/tour":       true,
	"x/vgo":        true,
	"x/website":    true,
	"x/xerrors":    true,
}

func adjustGoRepoInfo(repo string, modulePath string) (src, bugs string) {
	suffix := strings.TrimPrefix(modulePath, "golang.org/")

	// Validate that this is a repo that exists on
	// https://cs.opensource.google/go. Otherwise, default to the existing
	// info.
	parts := strings.Split(suffix, "/")
	if len(parts) >= 2 {
		suffix = parts[0] + "/" + parts[1]
	}
	if strings.HasPrefix(suffix, "x/") {
		if !csXRepos[suffix] {
			return repo, repo
		}
	} else if !csNonXRepos[suffix] {
		return repo, repo
	}

	return fmt.Sprintf("https://cs.opensource.google/go/%s", suffix), goIssuesURL
}

// matchStatic matches the given module or repo path against a list of known
// patterns. It returns the repo name if there is a match.
func matchStatic(moduleOrRepoPath string) (repo string, bugs func(string) string, _ error) {
	for _, pat := range patterns {
		matches := pat.re.FindStringSubmatch(moduleOrRepoPath)
		if matches == nil {
			continue
		}
		var repo string
		for i, n := range pat.re.SubexpNames() {
			if n == "repo" {
				repo = matches[i]
				break
			}
		}
		// Special case: git.apache.org has a go-import tag that points to
		// github.com/apache, but it's not quite right (the repo prefix is
		// missing a ".git"), so handle it here.
		const apacheDomain = "git.apache.org/"
		if strings.HasPrefix(repo, apacheDomain) {
			repo = strings.Replace(repo, apacheDomain, "github.com/apache/", 1)
		}
		// Special case: module paths are blitiri.com.ar/go/..., but repos are blitiri.com.ar/git/r/...
		if strings.HasPrefix(repo, "blitiri.com.ar/") {
			repo = strings.Replace(repo, "/go/", "/git/r/", 1)
		}
		return repo, pat.issues, nil
	}
	noop := func(s string) string { return s }
	return "", noop, errors.New("not found")
}

// Patterns for determining repo and URL transformation from module paths or repo
// URLs. Each regexp must match a prefix of the target string, and must have a
// group named "repo".
var patterns = []struct {
	pattern string // uncompiled regexp
	re      *regexp.Regexp
	issues  func(repo string) string
}{
	{
		pattern: `^(?P<repo>github\.com/[a-z0-9A-Z_.\-]+/[a-z0-9A-Z_.\-]+)`,
		issues:  func(repo string) string { return fmt.Sprintf("%s/issues", repo) },
	},
	{
		// Assume that any site beginning with "github." works like github.com.
		pattern: `^(?P<repo>github\.[a-z0-9A-Z.-]+/[a-z0-9A-Z_.\-]+/[a-z0-9A-Z_.\-]+)(\.git|$)`,
		issues:  func(repo string) string { return fmt.Sprintf("%s/issues", repo) },
	},
	{
		pattern: `^(?P<repo>bitbucket\.org/[a-z0-9A-Z_.\-]+/[a-z0-9A-Z_.\-]+)`,
		issues:  func(repo string) string { return fmt.Sprintf("%s/issues", repo) },
	},
	{
		pattern: `^(?P<repo>gitlab\.com/[a-z0-9A-Z_.\-]+/[a-z0-9A-Z_.\-]+)`,
		issues:  func(repo string) string { return fmt.Sprintf("%s/-/issues", repo) },
	},
	{
		// Assume that any site beginning with "gitlab." works like gitlab.com.
		pattern: `^(?P<repo>gitlab\.[a-z0-9A-Z.-]+/[a-z0-9A-Z_.\-]+/[a-z0-9A-Z_.\-]+)(\.git|$)`,
		issues:  func(repo string) string { return fmt.Sprintf("%s/-/issues", repo) },
	},
	{
		pattern: `^(?P<repo>gitee\.com/[a-z0-9A-Z_.\-]+/[a-z0-9A-Z_.\-]+)(\.git|$)`,
		issues:  func(repo string) string { return fmt.Sprintf("%s/issues", repo) },
	},
	{
		pattern: `^(?P<repo>git\.sr\.ht/~[a-z0-9A-Z_.\-]+/[a-z0-9A-Z_.\-]+)`,
		issues:  func(repo string) string { return strings.Replace(repo, "git.sr.ht", "todo.sr.ht", 1) },
	},
	{
		pattern: `^(?P<repo>git\.fd\.io/[a-z0-9A-Z_.\-]+)`,
		issues:  func(repo string) string { return repo },
	},
	{
		pattern: `^(?P<repo>git\.pirl\.io/[a-z0-9A-Z_.\-]+/[a-z0-9A-Z_.\-]+)`,
		issues:  func(repo string) string { return repo },
	},
	{
		pattern: `^(?P<repo>gitea\.com/[a-z0-9A-Z_.\-]+/[a-z0-9A-Z_.\-]+)(\.git|$)`,
		issues:  func(repo string) string { return fmt.Sprintf("%s/issues", repo) },
	},
	{
		// Assume that any site beginning with "gitea." works like gitea.com.
		pattern: `^(?P<repo>gitea\.[a-z0-9A-Z.-]+/[a-z0-9A-Z_.\-]+/[a-z0-9A-Z_.\-]+)(\.git|$)`,
		issues:  func(repo string) string { return fmt.Sprintf("%s/issues", repo) },
	},
	{
		pattern: `^(?P<repo>go\.isomorphicgo\.org/[a-z0-9A-Z_.\-]+/[a-z0-9A-Z_.\-]+)(\.git|$)`,
		issues:  func(repo string) string { return fmt.Sprintf("%s/issues", repo) },
	},
	{
		pattern: `^(?P<repo>git\.openprivacy\.ca/[a-z0-9A-Z_.\-]+/[a-z0-9A-Z_.\-]+)(\.git|$)`,
		issues:  func(repo string) string { return fmt.Sprintf("%s/issues", repo) },
	},
	{
		pattern: `^(?P<repo>gogs\.[a-z0-9A-Z.-]+/[a-z0-9A-Z_.\-]+/[a-z0-9A-Z_.\-]+)(\.git|$)`,
		issues:  func(repo string) string { return repo },
	},
	{
		pattern: `^(?P<repo>dmitri\.shuralyov\.com\/.+)$`,
		issues:  func(repo string) string { return fmt.Sprintf("%s$issues", repo) },
	},
	{
		pattern: `^(?P<repo>blitiri\.com\.ar/go/.+)$`,
		issues:  func(repo string) string { return "mailto:albertito@blitiri.com.ar" },
	},

	// Patterns that match the general go command pattern, where they must have
	// a ".git" repo suffix in an import path. If matching a repo URL from a meta tag,
	// there is no ".git".
	{
		pattern: `^(?P<repo>[^.]+\.googlesource\.com/[^.]+)(\.git|$)`,
		issues:  func(repo string) string { return repo },
	},
	{
		pattern: `^(?P<repo>git\.apache\.org/[^.]+)(\.git|$)`,
		issues:  func(repo string) string { return repo },
	},
	// General syntax for the go command. We can extract the repo and directory, but
	// we don't know the URL templates.
	// Must be last in this list.
	{
		pattern: `(?P<repo>([a-z0-9.\-]+\.)+[a-z0-9.\-]+(:[0-9]+)?(/~?[A-Za-z0-9_.\-]+)+?)\.(bzr|fossil|git|hg|svn)`,
		issues:  func(repo string) string { return repo },
	},
}

func init() {
	for i := range patterns {
		re := regexp.MustCompile(patterns[i].pattern)
		// The pattern regexp must contain a group named "repo".
		found := false
		for _, n := range re.SubexpNames() {
			if n == "repo" {
				found = true
				break
			}
		}
		if !found {
			panic(fmt.Sprintf("pattern %s missing <repo> group", patterns[i].pattern))
		}
		patterns[i].re = re
	}
}

// trimVCSSuffix removes a VCS suffix from a repo URL in selected cases.
//
// The Go command allows a VCS suffix on a repo, like github.com/foo/bar.git. But
// some code hosting sites don't support all paths constructed from such URLs.
// For example, GitHub will redirect github.com/foo/bar.git to github.com/foo/bar,
// but will 404 on github.com/goo/bar.git/tree/master and any other URL with a
// non-empty path.
//
// To be conservative, we remove the suffix only in cases where we know it's
// wrong.
func trimVCSSuffix(repoURL string) string {
	if !strings.HasSuffix(repoURL, ".git") {
		return repoURL
	}
	if strings.HasPrefix(repoURL, "https://github.com/") || strings.HasPrefix(repoURL, "https://gitlab.com/") {
		return strings.TrimSuffix(repoURL, ".git")
	}
	return repoURL
}

// removeHTTPScheme removes an initial "http://" or "https://" from url.
// The result can be used to match against our static patterns.
// If the URL uses a different scheme, it won't be removed and it won't
// match any patterns, as intended.
func removeHTTPScheme(url string) string {
	for _, prefix := range []string{"https://", "http://"} {
		if strings.HasPrefix(url, prefix) {
			return url[len(prefix):]
		}
	}
	return url
}

// sourceMeta represents the values in a go-source meta tag, or as a fallback,
// values from a go-import meta tag.
// The go-source spec is at https://github.com/golang/gddo/wiki/Source-Code-Links.
// The go-import spec is in "go help importpath".
type sourceMeta struct {
	repoRootPrefix string // import path prefix corresponding to repo root
	repoURL        string // URL of the repo root
}

// fetchMeta retrieves go-import and go-source meta tag information, using the import path to construct
// a URL as described in "go help importpath".
//
// The importPath argument, as the name suggests, could be any package import
// path. But we only pass module paths.
//
// The discovery site only cares about linking to source, not fetching it (we
// already have it in the module zip file). So we merge the go-import and
// go-source meta tag information, preferring the latter.
func fetchMeta(ctx context.Context, importPath string) (_ *sourceMeta, err error) {
	uri := importPath
	if !strings.Contains(uri, "/") {
		// Add slash for root of domain.
		uri = uri + "/"
	}
	uri = uri + "?go-get=1"

	var client http.Client
	resp, err := doURL(ctx, &client, "GET", "https://"+uri, true)
	if err != nil {
		resp, err = doURL(ctx, &client, "GET", "http://"+uri, false)
		if err != nil {
			return nil, err
		}
	}
	defer resp.Body.Close()
	return parseMeta(importPath, resp.Body)
}

// doURL makes an HTTP request using the given url and method. It returns an
// error if the request returns an error. If only200 is true, it also returns an
// error if any status code other than 200 is returned.
func doURL(ctx context.Context, client *http.Client, method, url string, only200 bool) (_ *http.Response, err error) {
	req, err := http.NewRequestWithContext(ctx, method, url, nil)
	if err != nil {
		return nil, err
	}
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	if only200 && resp.StatusCode != 200 {
		resp.Body.Close()
		return nil, fmt.Errorf("status %s", resp.Status)
	}
	return resp, nil
}

func parseMeta(importPath string, r io.Reader) (sm *sourceMeta, err error) {
	errorMessage := "go-import and go-source meta tags not found"
	// gddo uses an xml parser, and this code is adapted from it.
	d := xml.NewDecoder(r)
	d.Strict = false
metaScan:
	for {
		t, tokenErr := d.Token()
		if tokenErr != nil {
			break metaScan
		}
		switch t := t.(type) {
		case xml.EndElement:
			if strings.EqualFold(t.Name.Local, "head") {
				break metaScan
			}
		case xml.StartElement:
			if strings.EqualFold(t.Name.Local, "body") {
				break metaScan
			}
			if !strings.EqualFold(t.Name.Local, "meta") {
				continue metaScan
			}
			nameAttr := attrValue(t.Attr, "name")
			if nameAttr != "go-import" && nameAttr != "go-source" {
				continue metaScan
			}
			fields := strings.Fields(attrValue(t.Attr, "content"))
			if len(fields) < 1 {
				continue metaScan
			}
			repoRootPrefix := fields[0]
			if !strings.HasPrefix(importPath, repoRootPrefix) || (len(importPath) != len(repoRootPrefix) && importPath[len(repoRootPrefix)] != '/') {
				// Ignore if root is not a prefix of the path. This allows a
				// site to use a single error page for multiple repositories.
				continue metaScan
			}
			switch nameAttr {
			case "go-import":
				if len(fields) != 3 {
					errorMessage = "go-import meta tag content attribute does not have three fields"
					continue metaScan
				}
				if fields[1] == "mod" {
					// We can't make source links from a "mod" vcs type, so skip it.
					continue
				}
				if sm != nil {
					sm = nil
					errorMessage = "more than one go-import meta tag found"
					break metaScan
				}
				sm = &sourceMeta{
					repoRootPrefix: repoRootPrefix,
					repoURL:        fields[2],
				}
				// Keep going in the hope of finding a go-source tag.
			case "go-source":
				if len(fields) != 4 {
					errorMessage = "go-source meta tag content attribute does not have four fields"
					continue metaScan
				}
				if sm != nil && sm.repoRootPrefix != repoRootPrefix {
					errorMessage = fmt.Sprintf("import path prefixes %q for go-import and %q for go-source disagree", sm.repoRootPrefix, repoRootPrefix)
					sm = nil
					break metaScan
				}
				// If go-source repo is "_", then default to the go-import repo.
				repoURL := fields[1]
				if repoURL == "_" {
					if sm == nil {
						errorMessage = `go-source repo is "_", but no previous go-import tag`
						break metaScan
					}
					repoURL = sm.repoURL
				}
				sm = &sourceMeta{
					repoRootPrefix: repoRootPrefix,
					repoURL:        repoURL,
				}
				break metaScan
			}
		}
	}
	if sm == nil {
		return nil, fmt.Errorf("%s: %w", errorMessage, errors.New("not found"))
	}
	return sm, nil
}

func attrValue(attrs []xml.Attr, name string) string {
	for _, a := range attrs {
		if strings.EqualFold(a.Name.Local, name) {
			return a.Value
		}
	}
	return ""
}
