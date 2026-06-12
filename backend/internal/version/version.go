// Package version exposes the build identity of the running binary.
package version

// Version is the release identity of this build. The default "dev" marks a
// local or untagged build; release builds override it at link time via
// -ldflags "-X github.com/harveyxiacn/ZenithPanel/backend/internal/version.Version=v1.0.0"
// (wired through the Dockerfile VERSION build-arg in CI).
var Version = "dev"
