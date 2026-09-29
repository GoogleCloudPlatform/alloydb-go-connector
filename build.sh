#!/usr/bin/env bash

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

# Set SCRIPT_DIR to the current directory of this file.
SCRIPT_DIR=$(cd -P "$(dirname "$0")" >/dev/null 2>&1 && pwd)
SCRIPT_FILE="${SCRIPT_DIR}/$(basename "$0")"

if [[ -z "${GITHUB_TOKEN:-}" ]] && command -v gh &> /dev/null; then
  GITHUB_TOKEN=$(gh auth token 2>/dev/null || true)
  if [[ -n "$GITHUB_TOKEN" ]]; then
    export GITHUB_TOKEN
  fi
fi

##
## Local Development
##
## These functions should be used to run the local development process
##

## clean - Cleans the build output
function clean() {
  if [[ -d '.tools' ]] ; then
    rm -rf .tools
  fi
  if [[ -d 'test-results' ]] ; then
    rm -rf test-results
  fi
}

## build - Builds the project without running tests.
function build() {
  go build -buildvcs=false ./...
}

## test - Runs local unit tests.
function test() {
  # Install go tools
  get_golang_tool 'go-junit-report' 'jstemmer/go-junit-report' 'github.com/jstemmer/go-junit-report/v2'
  mkdir -p test-results

  local flags=("-v" "-cover" "-short")
  if [[ "${GOARCH:-}" != "386" ]]; then
    flags+=("-race")
  fi

  local args=( "./..." )
  if [[ "$#" -gt 0 ]] ; then
    args=( "$@" )
  fi

  export PATH="$SCRIPT_DIR/.tools:$PATH"
  go test "${flags[@]}" -json "${args[@]}" \
    | go-junit-report -iocopy -parser gojson -out test-results/unit.xml \
          | jq -j 'select(.Output) | .Output'
}

## e2e - Runs end-to-end integration tests.
function e2e() {
  if [[ -s .envrc ]] ; then
    source .envrc
  else
    write_e2e_env .envrc
    source .envrc
  fi
  e2e_ci "$@"
}

# e2e_ci - Run end-to-end integration tests in the CI system.
#   This assumes that the secrets in the env vars are already set.
function e2e_ci() {
  get_golang_tool 'go-junit-report' 'jstemmer/go-junit-report' 'github.com/jstemmer/go-junit-report/v2'
  export PATH="$SCRIPT_DIR/.tools:$PATH"
  mkdir -p test-results

  local args=( "./..." )
  if [[ "$#" -gt 0 ]] ; then
    args=( "$@" )
  fi

  go test -v -race -cover "${args[@]}" -json \
    | go-junit-report -iocopy -parser gojson -out test-results/e2e.xml \
    | jq -j 'select(.Output) | .Output '
}

# Download a tool using `go install`
function get_golang_tool() {
  name="$1"
  github_repo="$2"
  package="$3"

  local ext=""
  if [[ "$OSTYPE" == "msys" || "$OSTYPE" == "cygwin" || "$(uname -s)" == MINGW* ]]; then
    ext=".exe"
  fi

  mkdir -p "$SCRIPT_DIR/.tools"
  cmd="$SCRIPT_DIR/.tools/$name$ext"

  headers=()
  if [[ -n "${GITHUB_TOKEN:-}" ]]; then
    headers+=("-H" "Authorization: token ${GITHUB_TOKEN}")
  fi

  # Find latest version
  version=$(curl -s "${headers[@]}" "https://api.github.com/repos/$github_repo/tags" | jq -r '.[].name // empty' 2>/dev/null | head -n 1 || true)
  if [[ -z "$version" ]] ; then
    if [[ -x "$cmd" ]] ; then
      return 0
    fi
    version="latest"
  fi

  versioned_cmd="$SCRIPT_DIR/.tools/$name-$version$ext"
  if [[ ! -f "$versioned_cmd" ]] ; then
    (unset GOOS GOARCH; GOBIN="$SCRIPT_DIR/.tools" go install "$package@$version")
    if [[ -f "$cmd" && "$cmd" != "$versioned_cmd" ]]; then
      mv "$cmd" "$versioned_cmd"
      cp "$versioned_cmd" "$cmd" 2>/dev/null || ln -sf "$name-$version$ext" "$cmd"
    fi
  fi

  if [[ ! -e "$cmd" && -f "$versioned_cmd" ]] ; then
    cp "$versioned_cmd" "$cmd" 2>/dev/null || ln -sf "$name-$version$ext" "$cmd"
  fi
}

## fix - Fixes code format.
function fix() {
  # run code formatting
  get_golang_tool 'goimports' 'golang/tools' 'golang.org/x/tools/cmd/goimports'

  "$SCRIPT_DIR/.tools/goimports" -w .
  go mod tidy
  go fmt ./...
}

## lint - runs the linters
function lint() {
  # run lint checks
  get_golang_tool 'golangci-lint' 'golangci/golangci-lint' 'github.com/golangci/golangci-lint/v2/cmd/golangci-lint'
  export PATH="$SCRIPT_DIR/.tools:$PATH"
  go mod tidy && git diff --exit-code -- go.mod go.sum
  "$SCRIPT_DIR/.tools/golangci-lint" run --timeout 3m
}

# lint_ci - runs lint in the CI build job, exiting with an error code if lint fails.
function lint_ci() {
  fix # run code format cleanup
  git diff --exit-code # fail if anything changed
  lint # run lint
}

## deps - updates project dependencies to latest
function deps() {
  go get -u ./...
  go get -t -u ./...
  go mod tidy
}

# write_e2e_env - Loads secrets from the gcloud project and writes
#     them to target/e2e.env to run e2e tests.
function write_e2e_env(){
  # All secrets used by the e2e tests in the form <env_name>=<secret_name>
  secret_vars=(
    ALLOYDB_INSTANCE_NAME=ALLOYDB_INSTANCE_NAME
    ALLOYDB_PASS=ALLOYDB_CLUSTER_PASS
    ALLOYDB_INSTANCE_IP=ALLOYDB_INSTANCE_IP
    ALLOYDB_IAM_USER=ALLOYDB_GO_IAM_USER
    ALLOYDB_PSC_INSTANCE_URI=ALLOYDB_PSC_INSTANCE_URI
  )

  if [[ -z "${TEST_PROJECT:-}" ]] ; then
    echo "Set TEST_PROJECT environment variable to the project containing"
    echo "the e2e test suite secrets."
    exit 1
  fi

  echo "Getting test secrets from $TEST_PROJECT into $1"
  {
  echo "export ALLOYDB_DB='postgres'"
  echo "export ALLOYDB_USER='postgres'"
  for env_name in "${secret_vars[@]}" ; do
    env_var_name="${env_name%%=*}"
    secret_name="${env_name##*=}"
    val=$(gcloud secrets versions access latest --project "$TEST_PROJECT" --secret="$secret_name")
    echo "export $env_var_name='$val'"
  done
  } > "$1"
}

## help - prints the help details
##
function help() {
   # This will print the comments beginning with ## above each function
   # in this file.

   echo "build.sh <command> <arguments>"
   echo
   echo "Commands to assist with local development and CI builds."
   echo
   echo "Commands:"
   echo
   grep -e '^##' "$SCRIPT_FILE" | sed -e 's/##/ /'
}

set -euo pipefail

# Check CLI Arguments
if [[ "$#" -lt 1 ]] ; then
  help
  exit 1
fi

cd "$SCRIPT_DIR"

"$@"
