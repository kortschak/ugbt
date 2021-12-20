// Copyright ©2021 Dan Kortschak. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package main

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path"
	"regexp"
	"runtime/debug"
	"sort"
	"strings"
	"text/tabwriter"
	"time"

	"golang.org/x/mod/modfile"
	"golang.org/x/mod/module"
	"golang.org/x/mod/semver"
	"golang.org/x/sys/execabs"

	"github.com/kortschak/ugbt/internal/browser"
	"github.com/kortschak/ugbt/internal/modrepo"
	"github.com/kortschak/ugbt/internal/tool"
)

// ugbt is the main application as passed to tool.Main
// It handles the main command line parsing and dispatch to the sub commands.
type ugbt struct {
	// Core application flags
	Timeout time.Duration `flag:"timeout" help:"set timeout for operations (0 for no timeout)."`
	tool.Profile

	// The name of the binary, used in help and telemetry.
	name string

	// The working directory to run commands in.
	wd string

	// The environment variables to use.
	env []string
}

// newUggboot returns a new ugbt ready to run.
func newUggboot(name, wd string, env []string) *ugbt {
	if wd == "" {
		wd, _ = os.Getwd()
	}
	return &ugbt{
		name:    name,
		wd:      wd,
		env:     env,
		Timeout: 10 * time.Minute,
	}
}

// Name implements tool.Application returning the binary name.
func (u *ugbt) Name() string { return u.name }

// Usage implements tool.Application returning empty extra argument usage.
func (*ugbt) Usage() string { return "<command> [command-flags] [command-args]" }

// ShortHelp implements tool.Application returning the main binary help.
func (*ugbt) ShortHelp() string {
	return "The Ugg boot tool."
}

// DetailedHelp implements tool.Application returning the main binary help.
// This includes the short help for all the sub commands.
func (u *ugbt) DetailedHelp(f *flag.FlagSet) {
	fmt.Fprint(f.Output(), `
Available commands are:
`)
	for _, c := range u.commands() {
		fmt.Fprintf(f.Output(), "  %s: %v\n", c.Name(), c.ShortHelp())
	}
	fmt.Fprint(f.Output(), `
ugbt flags are:
`)
	f.PrintDefaults()
}

// Run takes the args after top level flag processing, and invokes the correct
// sub command as specified by the first argument.
// If no arguments are passed it will invoke the server sub command, as a
// temporary measure for compatibility.
func (u *ugbt) Run(ctx context.Context, args ...string) error {
	if len(args) == 0 {
		return tool.Run(ctx, &help{}, args)
	}
	if u.Timeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, u.Timeout)
		defer cancel()
	}
	command, args := args[0], args[1:]
	for _, c := range u.commands() {
		if c.Name() == command {
			return tool.Run(ctx, c, args)
		}
	}
	return tool.CommandLineErrorf("Unknown command %v", command)
}

// commands returns the set of commands supported by the ugbt tool on the
// command line.
// The command is specified by the first non flag argument.
func (u *ugbt) commands() []tool.Application {
	return []tool.Application{
		&list{ugbt: u},
		&install{ugbt: u},
		&repo{ugbt: u},
		&bugs{ugbt: u},
		&version{ugbt: u},
		&help{},
	}
}

// list implements the list command.
type list struct {
	*ugbt

	All        bool   `flag:"all" help:"list all versions not just unretracted and newer than the installed executable"`
	PreRelease string `flag:"suffix" help:"only print versions with a pre-release matching the regexp pattern"`
}

func (*list) Name() string      { return "list" }
func (*list) Usage() string     { return "[/path/to/go/executable]" }
func (*list) ShortHelp() string { return "runs the ugbt list command" }
func (*list) DetailedHelp(f *flag.FlagSet) {
	fmt.Fprint(f.Output(), `
The list command prints a list of available versions for the queried
executable including any retraction details. If the -all flag is given,
all versions including versions older that the current executable are
printed. If an executable path is not provided, ugbt will print ugbt
version information.

`)
	f.PrintDefaults()
}

// Run runs the ugbt list command.
func (l *list) Run(ctx context.Context, args ...string) error {
	var exe string
	switch len(args) {
	case 0:
		// Work on ugbt.
	case 1:
		exe = args[0]
	default:
		return errors.New("list requires zero or one argument")
	}

	suffix, err := regexp.Compile(l.PreRelease)
	if err != nil {
		return err
	}

	const defaultFormat = "_2 Jan 2006 15:04"
	format := defaultFormat

	_, mod, current, err := l.version(ctx, exe)
	if err != nil {
		return err
	}
	versions, err := l.availableVersions(ctx, mod, current, l.All)
	if err != nil {
		return err
	}
	w := tabwriter.NewWriter(os.Stdout, 0, 8, 2, ' ', tabwriter.DiscardEmptyColumns)
	var n int
	for _, v := range versions {
		if !l.All && semverCompare(v.Version, current) <= 0 {
			if n == 0 {
				fmt.Fprintln(os.Stderr, "no new version")
			}
			break
		}
		if !l.All && v.isRetracted {
			continue
		}
		if !suffix.MatchString(semver.Prerelease(v.Version)) {
			continue
		}
		fmt.Fprintf(w, "%s", v.Version)
		if !v.Time.IsZero() {
			fmt.Fprintf(w, "\t%s", v.Time.Format(format))
		}
		if v.isRetracted {
			if v.retractionRationale != "" {
				fmt.Fprintf(w, "\tretracted: %s", v.retractionRationale)
			} else {
				fmt.Fprint(w, "\tretracted")
			}
		}
		fmt.Fprintln(w)
		n++
	}
	return w.Flush()
}

func semverCompare(v, w string) int {
	return semver.Compare(replacePrefix(v, "go", "v"), replacePrefix(w, "go", "v"))
}

func replacePrefix(s, old, new string) string {
	if !strings.HasPrefix(s, old) {
		return s
	}
	return new + strings.TrimPrefix(s, old)
}

// install implements the install command.
type install struct {
	*ugbt

	Verbose  bool `flag:"v" help:"print the names of packages as they are compiled."`
	Commands bool `flag:"x" help:"print the commands run by the go tool."`
}

func (*install) Name() string      { return "install" }
func (*install) Usage() string     { return "[/path/to/go/executable] <version>" }
func (*install) ShortHelp() string { return "runs the ugbt install command" }
func (*install) DetailedHelp(f *flag.FlagSet) {
	fmt.Fprint(f.Output(), `
The install command reinstalls the executable at the provided path using
go install. Any valid version may be used including "latest". See 'go help get'.
If an executable path is not provided, ugbt will install the ugbt command
at the requested version.

If the executable is in the standard library, a golang.org/x/dl tool will
be used to download the SDK. When installing the SDK, "latest" refers to the
latest release. The "gotip" version will install the current development tip.

`)
	f.PrintDefaults()
}

// Run runs the ugbt install command.
func (i *install) Run(ctx context.Context, args ...string) error {
	var exe, version string
	switch len(args) {
	case 1:
		version = args[0]
	case 2:
		exe = args[0]
		version = args[1]
	default:
		return errors.New("install requires one or two arguments")
	}

	path, mod, _, err := i.version(ctx, exe)
	if err != nil {
		return err
	}
	return i.install(ctx, path, mod, version, i.Verbose, i.Commands)
}

// repo implements the repo command.
type repo struct {
	*ugbt

	Open bool `flag:"o" help:"open the repo url in a browser instead of printing it."`
}

func (*repo) Name() string      { return "repo" }
func (*repo) Usage() string     { return "[/path/to/go/executable]" }
func (*repo) ShortHelp() string { return "runs the ugbt repo command" }
func (*repo) DetailedHelp(f *flag.FlagSet) {
	fmt.Fprint(f.Output(), `
The repo command prints the source repo URL for the executable. If an
executable path is not provided, ugbt will print the ugbt repo.

`)
	f.PrintDefaults()
}

// Run runs the ugbt repo command.
func (r *repo) Run(ctx context.Context, args ...string) error {
	var exe string
	switch len(args) {
	case 0:
		// Work on ugbt.
	case 1:
		exe = args[0]
	default:
		return errors.New("repo requires zero or one argument")
	}

	_, mod, _, err := r.version(ctx, exe)
	if err != nil {
		return err
	}
	url, _, err := modrepo.URL(ctx, mod)
	if err != nil {
		return err
	}
	if !r.Open || !browser.Open(url) {
		fmt.Println(url)
	}
	return nil
}

// bugs implements the bugs command.
type bugs struct {
	*ugbt

	Open bool `flag:"o" help:"open the issues url in a browser instead of printing it."`
}

func (*bugs) Name() string      { return "bugs" }
func (*bugs) Usage() string     { return "[/path/to/go/executable]" }
func (*bugs) ShortHelp() string { return "runs the ugbt bugs command" }
func (*bugs) DetailedHelp(f *flag.FlagSet) {
	fmt.Fprint(f.Output(), `
The bugs command prints the URL for issues for the executable. If an executable
path is not provided, ugbt will print the ugbt bugs. If the issues URL is not
known, the source repo URL is printed.

`)
	f.PrintDefaults()
}

// Run runs the ugbt bugs command.
func (b *bugs) Run(ctx context.Context, args ...string) error {
	var exe string
	switch len(args) {
	case 0:
		// Work on ugbt.
	case 1:
		exe = args[0]
	default:
		return errors.New("bugs requires zero or one argument")
	}

	_, mod, _, err := b.version(ctx, exe)
	if err != nil {
		return err
	}
	_, url, err := modrepo.URL(ctx, mod)
	if err != nil {
		return err
	}
	if !b.Open || !browser.Open(url) {
		fmt.Println(url)
	}
	return nil
}

// version implements the version command.
type version struct {
	*ugbt

	// Enable verbose logging
	Verbose bool `flag:"v" help:"verbose output"`
}

func (*version) Name() string      { return "version" }
func (*version) Usage() string     { return "" }
func (*version) ShortHelp() string { return "print the ugbt version information" }
func (*version) DetailedHelp(f *flag.FlagSet) {
	f.PrintDefaults()
}

// Run prints ugbt version information.
func (v *version) Run(ctx context.Context, args ...string) error {
	printBuildInfo(os.Stdout, v.Verbose)
	return nil
}

func printBuildInfo(w io.Writer, verbose bool) {
	if info, ok := debug.ReadBuildInfo(); ok {
		fmt.Fprintf(w, "%v %v\n", info.Path, info.Main.Version)
		if verbose {
			for _, dep := range info.Deps {
				printModuleInfo(w, dep)
			}
		}
	} else {
		fmt.Fprintf(w, "version unknown, built in $GOPATH mode\n")
	}
}

func printModuleInfo(w io.Writer, m *debug.Module) {
	fmt.Fprintf(w, "    %s@%s", m.Path, m.Version)
	if m.Sum != "" {
		fmt.Fprintf(w, " %s", m.Sum)
	}
	if m.Replace != nil {
		fmt.Fprintf(w, " => %v", m.Replace.Path)
	}
	fmt.Fprintf(w, "\n")
}

// help implements the help command.
type help struct{}

func (*help) Name() string      { return "help" }
func (*help) Usage() string     { return "" }
func (*help) ShortHelp() string { return "output ugbt help information" }
func (*help) DetailedHelp(f *flag.FlagSet) {
	f.PrintDefaults()
}

// Run outputs the help text.
func (*help) Run(ctx context.Context, args ...string) error {
	fmt.Fprintf(os.Stdout, "%s", helpText)
	return nil
}

const helpText = `
The Ugg boot tool.

Usage: ugbt [flags] <command> [command-flags] [command-args]

Ugg boot provides a simple way to update Go executables and list
available versions using module version information embedded in
the executable.

Available commands:

  list: print a list of available versions for a Go executable

  install: install an executable from source based on source location
           information stored in the executable

  repo: print the source code repository URL for the executable

  bugs: print the issues URL for the executable

  version: print the ugbt version information

  help: output ugbt help information

Help for each command is provided with the -h flag.
`

// version returns the Go package path, mod path and version of the an
// executable.
func (u *ugbt) version(ctx context.Context, exepath string) (pth, mod, version string, err error) {
	if exepath == "" {
		info, ok := debug.ReadBuildInfo()
		if !ok {
			return "", "", "", errors.New("could not read build info")
		}
		// info.Path is being abused here, but it will work if the ugbt
		// command always lives at the root of the module.
		return info.Path, info.Main.Path, info.Main.Version, nil
	}

	exepath, err = exec.LookPath(exepath)
	if err != nil {
		return "", "", "", err
	}

	var stdout bytes.Buffer
	err = u.cmd(ctx, &stdout, nil, "version", "-m", exepath).Run()
	if err != nil {
		return "", "", "", err
	}
	var main string
	sc := bufio.NewScanner(&stdout)
	for sc.Scan() {
		if len(sc.Bytes()) == 0 {
			continue
		}
		if m := bytes.Split(sc.Bytes(), []byte(": ")); len(m) == 2 {
			main = string(m[0])
			version = string(m[1])
		}
		f := bytes.Fields(sc.Bytes())
		switch {
		case bytes.Equal(f[0], []byte("path")):
			if len(f) < 2 {
				return "", "", "", fmt.Errorf("unexpected path information format: %q", sc.Bytes())
			}
			pth = string(f[1])
		case bytes.Equal(f[0], []byte("mod")):
			if len(f) < 3 {
				return "", "", "", fmt.Errorf("unexpected module information format: %q", sc.Bytes())
			}
			mod = string(f[1])
			version = string(f[2])
		}
		if pth != "" && mod != "" && version != "" {
			return pth, mod, version, nil
		}
	}
	if sc.Err() != nil {
		return "", "", "", sc.Err()
	}
	if strings.HasPrefix(version, "go") {
		return path.Join("cmd", path.Base(main)), "std", version, nil
	}
	return "", "", "", errors.New("not a go binary or no module information")
}

// install installs the package at the given path at the given version.
func (u *ugbt) install(ctx context.Context, path, mod, version string, verbose, commands bool) error {
	if mod == "std" {
		return u.installStd(ctx, path, version, verbose, commands)
	}

	args := []string{"install"}
	if verbose {
		args = append(args, "-v")
	}
	if commands {
		args = append(args, "-x")
	}
	args = append(args, path+"@"+version)
	var buf bytes.Buffer
	stderr := io.Writer(&buf)
	if verbose || commands {
		stderr = io.MultiWriter(os.Stderr, stderr)
	}
	err := u.cmd(ctx, nil, stderr, args...).Run()
	if err != nil {
		if verbose || commands {
			return fmt.Errorf("go install: %w", err)
		}
		return errors.New(strings.TrimSpace(buf.String()))
	}
	return nil
}

// installStd installs the go tool chain and standard library.
func (u *ugbt) installStd(ctx context.Context, path, version string, verbose, commands bool) error {
	if version == "latest" {
		versions, err := u.stdInfo(ctx)
		if err != nil {
			return err
		}
		if len(versions) == 0 {
			return errors.New("not found")
		}
		version = versions[0].Version
	}
	err := u.install(ctx, "golang.org/dl/"+version, "", "latest", verbose, commands)
	if err != nil {
		return err
	}
	stderr := io.Discard
	if verbose {
		stderr = os.Stderr
	}
	cmd := execabs.CommandContext(ctx, version, "download")
	cmd.Dir = u.wd
	cmd.Stderr = stderr
	err = cmd.Run()
	if err != nil {
		return err
	}
	if !verbose {
		fmt.Fprintf(os.Stderr, "go tool available as %s\n", version)
	}
	return nil
}

type info struct {
	Version             string
	Time                time.Time
	isRetracted         bool
	retractionRationale string
}

// availableVersions returns the available semver versions from the
// $GOPROXY version database. Only versions at or after the current
// version are returned unless all is true.
func (t *ugbt) availableVersions(ctx context.Context, mod, current string, all bool) ([]info, error) {
	if mod == "std" {
		return t.stdInfo(ctx)
	}

	mod, err := module.EscapePath(mod)
	if err != nil {
		return nil, err
	}

	proxies, err := t.proxies(ctx)
	if err != nil {
		return nil, err
	}

	var (
		versions    []info
		cli         http.Client
		retractions []*modfile.Retract
	)
	for _, p := range proxies {
		u, err := url.Parse(p)
		if err != nil {
			return nil, err
		}
		u.Path = path.Join(mod, "@v", "list")
		req, err := http.NewRequest("GET", u.String(), nil)
		if err != nil {
			return nil, err
		}
		resp, err := cli.Do(req)
		if err != nil {
			return nil, err
		}
		defer resp.Body.Close()

		sc := bufio.NewScanner(resp.Body)
		var list []string
		for sc.Scan() {
			version := sc.Text()
			if all || semverCompare(version, current) >= 0 {
				list = append(list, version)
			}
		}
		for _, version := range list {
			u.Path = path.Join(mod, "@v", version)
			url := u.String()

			i, err := t.info(ctx, url)
			if err != nil {
				return nil, err
			}
			versions = append(versions, i)

			r, err := t.retractions(ctx, url)
			if err != nil {
				return nil, err
			}
			retractions = append(retractions, r...)
		}
	}
	versions = unique(versions)
	for i, v := range versions {
		for _, r := range retractions {
			if semver.Compare(v.Version, r.Low) >= 0 && semver.Compare(v.Version, r.High) <= 0 {
				versions[i].isRetracted = true
				versions[i].retractionRationale = r.Rationale
			}
		}
	}
	return versions, nil
}

// stdInfo returns the information for a Go standard library versions.
func (u *ugbt) stdInfo(ctx context.Context) ([]info, error) {
	buf, err := get(ctx, "https://go.dev/dl/?mode=json&include=all")
	if err != nil {
		return nil, fmt.Errorf("query proxy: %w", err)
	}
	var versions []info
	err = json.Unmarshal(buf, &versions)
	if err != nil {
		return nil, fmt.Errorf("invalid version information: %w", err)
	}
	sort.Slice(versions, func(i, j int) bool {
		return semverCompare(versions[i].Version, versions[j].Version) > 0
	})
	return versions, nil
}

// info returns the information for a version recorded by a Go proxy.
func (u *ugbt) info(ctx context.Context, version string) (info, error) {
	buf, err := get(ctx, version+".info")
	if err != nil {
		return info{}, fmt.Errorf("query proxy: %w", err)
	}
	var i info
	err = json.Unmarshal(buf, &i)
	if err != nil {
		return info{}, fmt.Errorf("invalid version information: %w", err)
	}
	return i, nil
}

// retractions returns any retractions noted in the version's modfile.
func (u *ugbt) retractions(ctx context.Context, version string) ([]*modfile.Retract, error) {
	buf, err := get(ctx, version+".mod")
	if err != nil {
		return nil, fmt.Errorf("query proxy: %w", err)
	}
	f, err := modfile.Parse(version+".mod", buf, nil)
	if err != nil {
		return nil, fmt.Errorf("invalid modfile: %w", err)
	}
	return f.Retract, nil
}

// get returns the body of a GET request to the provided URL. Any non 200
// response status is returned as an error.
func get(ctx context.Context, url string) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, "GET", url, nil)
	if err != nil {
		return nil, err
	}
	var cli http.Client
	resp, err := cli.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		io.Copy(io.Discard, resp.Body)
		return nil, statusError{status: resp.Status, code: resp.StatusCode}
	}
	var buf bytes.Buffer
	_, err = io.Copy(&buf, resp.Body)
	if err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

// statusError is an HTTP status error.
type statusError struct {
	status string
	code   int
}

func (e statusError) Error() string { return e.status }

// unique returns version lexically sorted in descending version order
// and with repeated elements omitted.
func unique(versions []info) []info {
	if len(versions) < 2 {
		return versions
	}
	sort.Slice(versions, func(i, j int) bool {
		return semver.Compare(versions[i].Version, versions[j].Version) > 0
	})
	curr := 0
	for i, addr := range versions {
		if addr == versions[curr] {
			continue
		}
		curr++
		if curr < i {
			versions[curr], versions[i] = versions[i], info{}
		}
	}
	return versions[:curr+1]
}

// proxies returns the list of GOPROXY proxies in go env.
func (u *ugbt) proxies(ctx context.Context) ([]string, error) {
	goproxy, err := u.goenv(ctx, "GOPROXY")
	if err != nil {
		return nil, err
	}
	var proxies []string
	for _, p := range strings.Split(goproxy, ",") {
		if p == "off" || p == "direct" {
			continue
		}
		proxies = append(proxies, p)
	}
	return proxies, nil
}

// goenv returns the requested go env variable.
func (u *ugbt) goenv(ctx context.Context, name string) (string, error) {
	var stdout, stderr bytes.Buffer
	err := u.cmd(ctx, &stdout, &stderr, "env", name).Run()
	if err != nil {
		return "", fmt.Errorf("%s: %w", &stderr, err)
	}
	return strings.TrimSpace(stdout.String()), nil
}

// cmd is a go command runner helper.
func (u *ugbt) cmd(ctx context.Context, stdout, stderr io.Writer, args ...string) *execabs.Cmd {
	cmd := execabs.CommandContext(ctx, "go", args...)
	cmd.Dir = u.wd
	cmd.Stdout = stdout
	cmd.Stderr = stderr
	return cmd
}
