#!/bin/bash
# Copyright 2026 Google LLC
#
# Licensed under the Apache License, Version 2.0 (the "License");
# you may not use this file except in compliance with the License.
# You may obtain a copy of the License at
#
#      http://www.apache.org/licenses/LICENSE-2.0
#
# Unless required by applicable law or agreed to in writing, software
# distributed under the License is distributed on an "AS IS" BASIS,
# WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
# See the License for the specific language governing permissions and
# limitations under the License.

# Requires environment variables: see .envrc.example.
set -euo pipefail

command -v go >/dev/null 2>&1 || { echo "go not found. Install from: https://go.dev/dl/" >&2; exit 1; }

if [[ $# -eq 0 ]]; then
  go test -v -race -cover ./...
elif [[ $# -eq 1 && "$1" == "--skip-private-ip" ]]; then
  # Only the root package defines the custom integration-test flag.
  go test -v -race -cover . -skip-private-ip
  root_package=$(go list .)
  all_packages=$(go list ./...)
  packages=()
  while IFS= read -r package; do
    if [[ "$package" != "$root_package" ]]; then
      packages+=("$package")
    fi
  done <<< "$all_packages"
  if [[ ${#packages[@]} -gt 0 ]]; then
    go test -v -race -cover "${packages[@]}"
  fi
else
  echo "Usage: $0 [--skip-private-ip]" >&2
  exit 1
fi
