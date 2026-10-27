module github.com/go-kanna/mapper/example

go 1.27.0

toolchain go1.27.3

// There is no require of github.com/go-kanna/mapper yet: it is unreleased, so
// this resolves through the repository's go.work. Once it carries a tag,
// running `go get -tool github.com/go-kanna/mapper/cmd/kanna-mapper` in your
// own module adds the require and the setup stands on its own.
tool github.com/go-kanna/mapper/cmd/kanna-mapper

require google.golang.org/protobuf v1.36.12
