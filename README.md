# Ugg Boot

Ugg boot is a tool for people wanting to have some comfort in their lives.

It provides a simple way to update Go executables and list available versions using module version information embedded in the executable.

- list: print a list of available versions for a Go executable.
- install: reinstall or update an executable from source.
- repo: print the source code repository for the executable.
- bugs: print the issues link for the executable.

## Installation

Ugg boot can be installed by `go install github.com/kortschak/ugbt@latest`.

## Example Use

### Go executable:

Show the repo for an executable.
```
$ ugbt repo goimports
https://cs.opensource.google/go/x/tools
```

List all available released versions.
```
$ ugbt list -all goimports
v0.1.7  28 Sep 2021 22:34
v0.1.6  17 Sep 2021 17:58
v0.1.5  13 Jul 2021 20:15
v0.1.4  23 Jun 2021 15:16
v0.1.3   9 Jun 2021 21:40
v0.1.2  25 May 2021 19:05
v0.1.1  11 May 2021 17:48
v0.1.0  19 Jan 2021 22:25
```

Upgrade to the latest version.
```
$ go version -m $(which goimports) | grep -v dep
$GOBIN/goimports: go1.17.3
	path	golang.org/x/tools/cmd/goimports
	mod	golang.org/x/tools	v0.1.6	h1:SIasE1FVIQOWz2GEAHFOmoW7xchJcqlucjSULTL0Ag4=
$ ugbt install goimports latest
$ go version -m $(which goimports) | grep -v dep
$GOBIN/goimports: go1.17.3
	path	golang.org/x/tools/cmd/goimports
	mod	golang.org/x/tools	v0.1.7	h1:6j8CgantCy3yc8JGBqkDLMKWqZ0RDU2g1HVgacojGWQ=
```

### Go toolchain:

Show the repo for the tool chain.
```
$ ugbt repo go
https://github.com/golang/go
```

Install gotip.
```
$ ugbt install go gotip
go tool available as gotip
$ gotip version
go version devel go1.18-a142d65 Sat Nov 27 19:49:32 2021 +0000 linux/amd64
```
---

Ugg boot is a product of Australia.