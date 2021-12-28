// Copyright ©2021 Dan Kortschak. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// Ugg boot provides a simple way to update Go executables and list
// available versions using module version information embedded in
// the executable.
//
// Available commands are:
//   list: return a list of available versions for a Go executable.
//   install: install an executable from source based on source location
//            information stored in the executable.
//   update: update an executable to the latest release if it is newer
//           than the installed version.
//   repo: print the source code repository for the executable.
//   bugs: print the issues link for the executable.
//   version: print the ugbt version information
//   help: output ugbt help information
//
package main

import (
	"context"
	"os"

	"github.com/kortschak/ugbt/internal/tool"
)

func main() {
	tool.Main(context.Background(), newUggboot(os.Args[0], "", nil), os.Args[1:])
}
