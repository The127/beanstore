#!/usr/bin/env bash
# Enforces the CLAUDE.md rule that package doc comments live in doc.go.
set -euo pipefail

fail=0
while IFS= read -r f; do
    [ "$(basename "$f")" = "doc.go" ] && continue
    if grep -B1 '^package ' "$f" | head -n1 | grep -q '^//'; then
        echo "$f: package doc comment belongs in doc.go"
        fail=1
    fi
done < <(git ls-files '*.go')
exit $fail
