#!/usr/bin/env sh
# SPDX-License-Identifier: Apache-2.0
# Copyright © 2026 Eldara Tech

set -eu

# What an installed swarmcli reports about itself, checked against what the
# channel it came from should have delivered.
#
#   EXPECT_VERSION=2.2.0 expect-installed-version.sh business swarmcli
#   expect-installed-version.sh oss docker run --rm eldaratech/swarmcli:v2.2.0-oss
#
# The command is run with `version` appended. An empty EXPECT_VERSION skips
# the version comparison but not the build check.

build=$1
shift
out=$("$@" version)
echo "$out"

case $out in
  *"$build build"*) ;;
  *) echo "::error::expected a $build build, got: $out"; exit 1 ;;
esac
case $out in
  *unstamped*) echo "::error::the chart engine version was not stamped: $out"; exit 1 ;;
esac
if [ -n "${EXPECT_VERSION:-}" ] && [ "${out%% *}" != "$EXPECT_VERSION" ]; then
  echo "::error::expected $EXPECT_VERSION, got: $out"
  exit 1
fi
