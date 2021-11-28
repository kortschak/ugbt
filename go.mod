module github.com/kortschak/ugbt

go 1.17

require (
	golang.org/x/mod v0.5.1
	golang.org/x/sys v0.0.0-20211124211545-fe61309f8881
)

require golang.org/x/xerrors v0.0.0-20191011141410-1b5146add898 // indirect

retract v1.0.0 // Unsafe use of os/exec.
