#!/bin/sh
set -eu
# Bootstrap helpers and compiler children use only the declared system path.
export PATH=/usr/bin:/bin
# The bootstrap compiler is explicit and constrained before compiling the
# manifest/installer. Tuple admission itself belongs to the compiled manifest.
go_bin=
need_go=0
for argument in "$@"; do
  if test "$need_go" = 1; then go_bin=$argument; need_go=0; continue; fi
  case "$argument" in --go-bin) need_go=1 ;; --go-bin=*) go_bin=${argument#--go-bin=} ;; esac
done
case "$go_bin" in /*) ;; *) echo '--go-bin must name an explicit absolute Go 1.26.7 executable' >&2; exit 2 ;; esac
test -x "$go_bin" || { echo 'explicit Go executable unavailable' >&2; exit 2; }
root=$(CDPATH= cd -- "$(dirname -- "$0")/.." && pwd -P)
export GOENV=off GOWORK=off GOTOOLCHAIN=local GOPROXY=off GOSUMDB=off
unset GOROOT GOTOOLDIR
export GOFLAGS= GOEXPERIMENT= GOOS=linux GOARCH=amd64 GOAMD64=v1 CGO_ENABLED=0
version=$("$go_bin" version)
test "$version" = 'go version go1.26.7 linux/amd64' || { echo 'bootstrap Go toolchain/target not qualified' >&2; exit 2; }
exec "$go_bin" -C "$root" run -trimpath -buildvcs=false -mod=readonly -ldflags=-s\ -w ./internal/testcampaign/cmd/hubnot-qualification install "$@"
