module github.com/kortschak/ugbt

go 1.17

require (
	golang.org/x/mod v0.5.1
	golang.org/x/sys v0.0.0-20211124211545-fe61309f8881
)

retract v1.0.0 // Unsafe use of os/exec.
