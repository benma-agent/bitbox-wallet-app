# SPDX-License-Identifier: Apache-2.0

VERSION_MK_PATH := $(abspath $(lastword $(MAKEFILE_LIST)))
VERSION_MK_ROOT := $(dir $(VERSION_MK_PATH))

APP_VERSION := $(strip $(shell cat "$(VERSION_MK_ROOT)APP_VERSION"))
GO_VERSION_LDFLAGS := -X github.com/BitBoxSwiss/bitbox-wallet-app/backend/versioninfo.versionString=$(APP_VERSION)
# Go's build ID can differ between machines even when the linked code is identical.
GO_RELEASE_LDFLAGS := $(GO_VERSION_LDFLAGS) -buildid=
