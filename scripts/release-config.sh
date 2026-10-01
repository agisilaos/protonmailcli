#!/usr/bin/env bash
# Repository-owned settings; shared helpers are pinned to this reviewed bundle.
CLI_NAME="protonmailcli"
FORMULA_NAME="protonmailcli"
ARTIFACT_NAME="protonmailcli"
DEFAULT_BRANCH="master"
DEFAULT_HOMEBREW_DESC="Proton Mail Bridge CLI for terminal workflows"
DEFAULT_HOMEBREW_LICENSE="MIT"
DEFAULT_HOMEBREW_TEST_ARG="--version"
DEFAULT_FORMULA_PATH="Formula/protonmailcli.rb"
DEFAULT_BUILD_PKG="./cmd/protonmailcli"
RELEASE_LDFLAGS_TEMPLATE='-s -w -X protonmailcli/internal/app.Version={{VERSION}} -X protonmailcli/internal/app.Commit={{COMMIT}} -X protonmailcli/internal/app.Date={{DATE}}'
RELEASE_VERSION_TEMPLATE='protonmailcli {{VERSION}} ({{COMMIT}}) {{DATE}}'
RELEASE_CGO_ENABLED=0
RELEASE_INCLUDE_LICENSE=0
CLI_TEMPLATE_FINGERPRINT="816f217b3a5c95477b24e3fb8ba1f3db5470654441b71b16e5385c9cb5b73290"
RELEASE_GO_TOOLCHAIN="go1.27.1"
