#!/usr/bin/env bash
# Capability coverage matrix check. See doc/capability-coverage.yaml.
#   ./scripts/check-capability-coverage.sh            report (always exits 0 unless a ref is malformed)
#   ./scripts/check-capability-coverage.sh --strict   fail on any gap/partial row or missing test
#   ./scripts/check-capability-coverage.sh --deep     also verify Go tests with `go test -list`
#   ./scripts/check-capability-coverage.sh --emit-md  print the markdown table
exec python3 "$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)/check-capability-coverage.py" "$@"
