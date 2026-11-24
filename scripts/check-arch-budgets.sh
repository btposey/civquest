#!/usr/bin/env bash
set -euo pipefail

REPO_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"

STRICT=0
for arg in "$@"; do
  case "$arg" in
    --strict)
      STRICT=1
      ;;
    -h|--help)
      echo "Usage: $0 [--strict]"
      echo "  Reports baseline line counts for key Go/Java files and struct field counts for AgentState & AgentRuntime."
      echo "  --strict  Exit non-zero if any file line count or struct field count exceeds its recorded baseline ceiling."
      exit 0
      ;;
    *)
      echo "Unknown option: $arg" >&2
      exit 2
      ;;
  esac
done

# Format: "relative_path|baseline_max_lines"
FILE_BUDGETS=(
  "orchestrator-go/internal/agent/runtime.go|4582"
  "orchestrator-go/internal/system1/fsm.go|2267"
  "orchestrator-go/internal/system1/bootstrap.go|940"
  "orchestrator-go/internal/system1/state.go|1823"
  "mod-neoforge/src/main/java/com/civquest/mod/actuator/IntentActuator.java|1999"
  "mod-neoforge/src/main/java/com/civquest/mod/actuator/AgentNavigator.java|1434"
  "mod-neoforge/src/main/java/com/civquest/mod/AgentEntity.java|1003"
)

# Format: "relative_path|struct_name|baseline_max_total_fields"
STRUCT_BUDGETS=(
  "orchestrator-go/internal/system1/state.go|AgentState|102"
  "orchestrator-go/internal/agent/runtime.go|AgentRuntime|98"
)

count_go_struct_fields() {
  local file="$1"
  local struct_name="$2"
  awk -v sname="$struct_name" '
    BEGIN { in_struct = 0; depth = 0; exported = 0; priv = 0 }
    $0 ~ "^type[[:space:]]+" sname "[[:space:]]+struct[[:space:]]*\\{" {
      in_struct = 1
      depth = 1
      next
    }
    in_struct {
      line = $0
      sub(/\/\/.*$/, "", line)
      gsub(/^[[:space:]]+|[[:space:]]+$/, "", line)
      if (line == "") next
      if (line ~ /^}/) {
        depth--
        if (depth <= 0) {
          in_struct = 0
          exit
        }
        next
      }
      if (depth == 1) {
        # Count each field line at top level of struct (handle multi-name declarations like a, b int)
        # Strip struct tags and type suffix after first space/tab if not embedded
        split(line, parts, /[[:space:]]+/)
        fname = parts[1]
        if (fname != "" && fname !~ /^\{/ && fname !~ /^\}/) {
          # Handle comma-separated field names on a single line (e.g. "x, y int")
          n = split(line, comma_parts, ",")
          # Only treat commas as multi-field if they appear before the type (no space before comma)
          multi = 1
          for (i = 1; i < n; i++) {
            gsub(/^[[:space:]]+|[[:space:]]+$/, "", comma_parts[i])
            if (comma_parts[i] ~ /^[A-Za-z_][A-Za-z0-9_]*$/) {
              multi++
            } else {
              multi = 1
              break
            }
          }
          if (multi > 1) {
            for (i = 1; i <= multi; i++) {
              f = comma_parts[i]
              gsub(/^[[:space:]]+|[[:space:]]+$/, "", f)
              if (i == multi) {
                split(f, tail_parts, /[[:space:]]+/)
                f = tail_parts[1]
              }
              if (substr(f, 1, 1) ~ /^[A-Z]/) exported++
              else priv++
            }
          } else {
            if (substr(fname, 1, 1) ~ /^[A-Z]/) exported++
            else priv++
          }
        }
      }
      if (line ~ /\{[[:space:]]*$/) depth++
    }
    END {
      printf "%d %d %d\n", exported + priv, exported, priv
    }
  ' "$file"
}

violations=0

echo "=== CivQuest Architecture Size & Complexity Baseline ==="
printf "%-72s %8s %10s %8s\n" "File" "Lines" "Baseline" "Status"
printf "%-72s %8s %10s %8s\n" "----" "-----" "--------" "------"

for entry in "${FILE_BUDGETS[@]}"; do
  IFS='|' read -r rel_path budget <<< "$entry"
  abs_path="${REPO_ROOT}/${rel_path}"
  if [[ ! -f "$abs_path" ]]; then
    printf "%-72s %8s %10s %8s\n" "$rel_path" "MISSING" "$budget" "FAIL"
    violations=$((violations + 1))
    continue
  fi
  lines=$(wc -l < "$abs_path" | tr -d '[:space:]')
  status="OK"
  if (( lines > budget )); then
    status="OVER"
    if (( STRICT == 1 )); then
      violations=$((violations + 1))
    fi
  fi
  printf "%-72s %8d %10d %8s\n" "$rel_path" "$lines" "$budget" "$status"
done

echo ""
echo "=== Struct Field Summary (Go) ==="
printf "%-20s %8s %10s %10s %10s %8s\n" "Struct" "Total" "Exported" "Private" "Baseline" "Status"
printf "%-20s %8s %10s %10s %10s %8s\n" "------" "-----" "--------" "-------" "--------" "------"

for entry in "${STRUCT_BUDGETS[@]}"; do
  IFS='|' read -r rel_path sname budget <<< "$entry"
  abs_path="${REPO_ROOT}/${rel_path}"
  read -r total exported priv < <(count_go_struct_fields "$abs_path" "$sname")
  status="OK"
  if (( total > budget )); then
    status="OVER"
    if (( STRICT == 1 )); then
      violations=$((violations + 1))
    fi
  fi
  printf "%-20s %8d %10d %10d %10d %8s\n" "$sname" "$total" "$exported" "$priv" "$budget" "$status"
done

if (( STRICT == 1 && violations > 0 )); then
  echo "" >&2
  echo "STRICT BUDGET CHECK FAILED: $violations violation(s) exceeded baseline." >&2
  exit 1
fi

exit 0
