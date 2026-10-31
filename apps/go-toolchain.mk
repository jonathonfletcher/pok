# apps/go-toolchain.mk — ONE definition of "which Go builds the images", included by apps/Makefile
# and every app Makefile (`include ../go-toolchain.mk`) so a direct `make -C apps/<app> image`
# and the aggregate `make -C apps images` produce the same binary for the same tag.
#
# Policy: build with the newest stable Go. go.dev/VERSION returns e.g. "go1.27.3"; exporting it
# as GOTOOLCHAIN makes `go`/`ko` use that release (downloaded on first use). "+auto" lets a go.mod
# that demands something newer win. Offline / go.dev unreachable -> "auto" (whatever go.mod names).
#
# Only targets that run the Go toolchain export it (`image: export GOTOOLCHAIN = $(GO_TOOLCHAIN)`),
# and GO_LATEST is resolved lazily and memoised, so `make -n`, `help`, `apply`, `deploy` never
# touch the network. A sub-make inherits the parent's resolved GOTOOLCHAIN instead of re-resolving.
# Override: make GO_LATEST=go1.27.1   or   GOTOOLCHAIN=local make ...
ifeq ($(origin GOTOOLCHAIN),environment)
GO_TOOLCHAIN := $(GOTOOLCHAIN)
else
GO_LATEST ?= $(eval GO_LATEST := $(shell curl -fsSL --max-time 5 'https://go.dev/VERSION?m=text' 2>/dev/null | head -1))$(GO_LATEST)
GO_TOOLCHAIN = $(if $(GO_LATEST),$(GO_LATEST)+auto,auto)
endif

# ko installs into $(go env GOPATH)/bin, which may not be on PATH. Recursive (=) so the
# `go env` call only happens when a recipe actually references $(KO).
KO = $(shell go env GOPATH)/bin/ko
