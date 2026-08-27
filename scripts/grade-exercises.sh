#!/usr/bin/env bash
set -euo pipefail

mode="${1:-student}"
chapters=(
  "./exercises/00-overview/..."
  "./exercises/01-user-auth/..."
  "./exercises/02-payment-up/..."
  "./exercises/03-payment-down/..."
  "./exercises/04-payment-clearing/..."
  "./exercises/05-payment-settlement/..."
  "./exercises/07-product-search/..."
)
race_student_packages=(
  "./exercises/05-payment-settlement/05.03-concurrent-idempotent-settlement/problem"
  "./exercises/05-payment-settlement/05.04-refund-settlement-race/problem"
)
race_solution_packages=(
  "./exercises/05-payment-settlement/05.03-concurrent-idempotent-settlement/solution"
  "./exercises/05-payment-settlement/05.04-refund-settlement-race/solution"
)

case "$mode" in
  student)
    packages=()
    for chapter in "${chapters[@]}"; do packages+=("${chapter}/problem"); done
    go test -tags exercise "${packages[@]}"
	go test -race -tags exercise "${race_student_packages[@]}"
    ;;
  solution)
    packages=()
    for chapter in "${chapters[@]}"; do packages+=("${chapter}/solution"); done
    go test -tags exercise "${packages[@]}"
	go test -race -tags exercise "${race_solution_packages[@]}"
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
