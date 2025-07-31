#!/usr/bin/env bash

cd "$(dirname "$0")"
. common.inc.sh

run_cmd go mod download
run_cmd yarn install
