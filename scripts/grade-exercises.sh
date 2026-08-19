#!/usr/bin/env bash
set -euo pipefail

mode="${1:-student}"
chapters=(
  "./exercises/00-overview/..."
  "./exercises/01-user-auth/..."
  "./exercises/02-payment-up/..."
  "./exercises/03-payment-down/..."
  "./exercises/04-payment-clearing/..."
)

case "$mode" in
  student)
    packages=()
    for chapter in "${chapters[@]}"; do packages+=("${chapter}/problem"); done
    go test -tags exercise "${packages[@]}"
    ;;
  solution)
    packages=()
    for chapter in "${chapters[@]}"; do packages+=("${chapter}/solution"); done
    go test -tags exercise "${packages[@]}"
    ;;
  compile)
    packages=()
    for chapter in "${chapters[@]}"; do packages+=("${chapter}/problem" "${chapter}/solution"); done
    go test -run '^$' -tags exercise "${packages[@]}"
    ;;
  *)
    echo "usage: $0 {student|solution|compile}" >&2
    exit 2
    ;;
esac
