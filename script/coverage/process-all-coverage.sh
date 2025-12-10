#!/usr/bin/env bash

#   Copyright The containerd Authors.

#   Licensed under the Apache License, Version 2.0 (the "License");
#   you may not use this file except in compliance with the License.
#   You may obtain a copy of the License at

#       http://www.apache.org/licenses/LICENSE-2.0

#   Unless required by applicable law or agreed to in writing, software
#   distributed under the License is distributed on an "AS IS" BASIS,
#   WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
#   See the License for the specific language governing permissions and
#   limitations under the License.

# Script to process coverage files and generate summary reports
#
# Usage:
#   Single platform mode:  process-all-coverage.sh <os> <cgroup_driver> <branch_name>
#   Overall mode:          process-all-coverage.sh --overall
#
# Examples:
#   ./process-all-coverage.sh ubuntu-24.04 cgroupfs my-branch
#   ./process-all-coverage.sh --overall

set -euo pipefail

# Check if running in overall mode
if [ "${1:-}" = "--overall" ]; then
    MODE="overall"
else
    MODE="single"
    OS="${1:?OS required (e.g., ubuntu-24.04)}"
    CGROUP_DRIVER="${2:?Cgroup driver required (e.g., cgroupfs, systemd)}"
    BRANCH_NAME="${3:?Branch name required}"
fi

# =============================================================================
# Helper Functions
# =============================================================================

# Function to detect if coverage file is in raw or summarized format
is_raw_format() {
    local file=$1
    local first_line=$(head -2 "$file" | tail -1)

    # Raw format: "file.go:10.2,20.3 5 10" (line:col numbers followed by statement/count)
    # Summarized format: "file.go:10: functionName 85.5%" (has function names and percentages)
    if echo "$first_line" | grep -qE '%|[[:space:]]{2,}'; then
        return 1  # summarized format
    else
        return 0  # raw format
    fi
}

# Function to extract coverage percentage from a file
get_coverage_percentage() {
    local file=$1
    local coverage_pct=""

    if is_raw_format "$file"; then
        # Use go tool cover for raw format
        coverage_pct=$(go tool cover -func="$file" 2>/dev/null | grep "total:" | awk '{print $3}' || echo "")
    else
        # Try to find total line in summarized format
        coverage_pct=$(grep 'total:' "$file" | awk '{print $NF}' || echo "")

        if [ -z "$coverage_pct" ]; then
            # Try go tool cover as fallback
            coverage_pct=$(go tool cover -func="$file" 2>/dev/null | grep "total:" | awk '{print $3}' || echo "")
        fi
    fi

    echo "$coverage_pct"
}

# =============================================================================
# Single Platform Mode
# =============================================================================
if [ "$MODE" = "single" ]; then
    echo "Processing coverage for $OS-$CGROUP_DRIVER"

    # Define coverage file mappings
    declare -A COVERAGE_FILES=(
        ["unit"]="coverage-unit-test-${OS}-${CGROUP_DRIVER}-${BRANCH_NAME}.txt"
        ["integration"]="coverage-integration-${OS}-${CGROUP_DRIVER}-${BRANCH_NAME}.txt"
        ["cri-integration"]="coverage-cri-integration-${OS}-${CGROUP_DRIVER}-${BRANCH_NAME}.txt"
        ["cri-tools-critest"]="coverage-cri-tools-critest-${OS}-${CGROUP_DRIVER}-${BRANCH_NAME}.txt"
    )

    # Create coverage summary files
    COVERAGE_SUMMARY="coverage-summary-${OS}-${CGROUP_DRIVER}.md"
    echo "## Coverage Summary (${OS}-${CGROUP_DRIVER})" > "$COVERAGE_SUMMARY"
    echo "" >> "$COVERAGE_SUMMARY"
    echo "| Test Type | Coverage % | Status |" >> "$COVERAGE_SUMMARY"
    echo "|-----------|------------|---------|" >> "$COVERAGE_SUMMARY"

    # Also output to GitHub step summary if available
    if [ -n "${GITHUB_STEP_SUMMARY:-}" ]; then
        echo "## Coverage Summary (${OS}-${CGROUP_DRIVER})" >> "$GITHUB_STEP_SUMMARY"
        echo "" >> "$GITHUB_STEP_SUMMARY"
        echo "| Test Type | Coverage % | Status |" >> "$GITHUB_STEP_SUMMARY"
        echo "|-----------|------------|---------|" >> "$GITHUB_STEP_SUMMARY"
    fi

    # Initialize combined coverage file
    COMBINED="all-coverage-${OS}-${CGROUP_DRIVER}-${BRANCH_NAME}.txt"
    echo "mode: atomic" > "$COMBINED"

    FOUND_FILES=0

    # Process each coverage file
    for test_type in "${!COVERAGE_FILES[@]}"; do
        file="${COVERAGE_FILES[$test_type]}"

        if [ -f "$file" ]; then
            echo "Processing $test_type coverage from $file"
            FOUND_FILES=$((FOUND_FILES + 1))

            # Get coverage percentage
            coverage_pct=$(get_coverage_percentage "$file")

            if [ -z "$coverage_pct" ]; then
                coverage_pct="N/A"
            fi

            # Add to summary
            echo "| $test_type | $coverage_pct | Found |" >> "$COVERAGE_SUMMARY"
            [ -n "${GITHUB_STEP_SUMMARY:-}" ] && echo "| $test_type | $coverage_pct | Found |" >> "$GITHUB_STEP_SUMMARY"

            # Add to combined file (only raw format files can be combined)
            if is_raw_format "$file"; then
                grep -h -v '^mode:' "$file" >> "$COMBINED"
            fi
        else
            echo "| $test_type | N/A | ❌ Missing |" >> "$COVERAGE_SUMMARY"
            [ -n "${GITHUB_STEP_SUMMARY:-}" ] && echo "| $test_type | N/A | ❌ Missing |" >> "$GITHUB_STEP_SUMMARY"
        fi
    done

    # Check if we found any files
    if [ $FOUND_FILES -eq 0 ]; then
        echo "" >> "$COVERAGE_SUMMARY"
        echo "**No coverage files found!** Check if previous test steps completed successfully." >> "$COVERAGE_SUMMARY"
        [ -n "${GITHUB_STEP_SUMMARY:-}" ] && {
            echo "" >> "$GITHUB_STEP_SUMMARY"
            echo "**No coverage files found!** Check if previous test steps completed successfully." >> "$GITHUB_STEP_SUMMARY"
        }
        exit 0
    fi

    # Calculate overall coverage from combined file
    if [ -s "$COMBINED" ] && [ $(wc -l < "$COMBINED") -gt 1 ]; then
        overall_coverage=$(go tool cover -func="$COMBINED" 2>/dev/null | grep "total:" | awk '{print $3}' || echo "Unable to calculate")

        echo "" >> "$COVERAGE_SUMMARY"
        echo "### Overall Coverage: $overall_coverage" >> "$COVERAGE_SUMMARY"
        echo "" >> "$COVERAGE_SUMMARY"
        echo "_Note: Overall coverage combines all test types (unit, integration, cri-integration, cri-tools-critest) in raw coverage format._" >> "$COVERAGE_SUMMARY"

        [ -n "${GITHUB_STEP_SUMMARY:-}" ] && {
            echo "" >> "$GITHUB_STEP_SUMMARY"
            echo "### Overall Coverage: $overall_coverage" >> "$GITHUB_STEP_SUMMARY"
            echo "" >> "$GITHUB_STEP_SUMMARY"
            echo "_Note: Overall coverage combines all test types (unit, integration, cri-integration, cri-tools-critest) in raw coverage format._" >> "$GITHUB_STEP_SUMMARY"
        }
    fi

    echo "Coverage summary saved to $COVERAGE_SUMMARY"
    echo "Combined coverage saved to $COMBINED"

# =============================================================================
# Overall Mode - Aggregate across all platforms
# =============================================================================
elif [ "$MODE" = "overall" ]; then
    echo "Processing overall coverage across all platforms..."

    # Initialize output files
    OVERALL_PR_COMMENT_FILE="pr-overall-coverage-comment.md"
    echo "## 🎯 Overall Coverage Summary" > "$OVERALL_PR_COMMENT_FILE"
    echo "" >> "$OVERALL_PR_COMMENT_FILE"
    echo "| Platform | Configuration | Coverage |" >> "$OVERALL_PR_COMMENT_FILE"
    echo "|----------|---------------|----------|" >> "$OVERALL_PR_COMMENT_FILE"

    # Also write to GitHub Step Summary if available
    if [ -n "${GITHUB_STEP_SUMMARY:-}" ]; then
        echo "## 🎯 Overall Coverage Summary" >> "$GITHUB_STEP_SUMMARY"
        echo "" >> "$GITHUB_STEP_SUMMARY"
        echo "| Platform | Configuration | Coverage |" >> "$GITHUB_STEP_SUMMARY"
        echo "|----------|---------------|----------|" >> "$GITHUB_STEP_SUMMARY"
    fi

    # Initialize grand total coverage file
    GRAND_TOTAL="grand-total-coverage.txt"
    echo "mode: atomic" > "$GRAND_TOTAL"

    # Process each platform/config combination
    TOTAL_CONFIGS=0
    COVERAGE_SUM=0

    for combined_file in all-coverage-*.txt; do
        if [ ! -f "$combined_file" ]; then
            continue
        fi

        # Extract platform and config from filename
        basename_file=$(basename "$combined_file" .txt)
        platform_config=$(echo "$basename_file" | sed 's/^all-coverage-//' | sed 's/-[^-]*$//')

        # Get coverage percentage
        if [ -s "$combined_file" ]; then
            coverage_pct=$(go tool cover -func="$combined_file" | grep total: | awk '{print $3}' | sed 's/%//')
            coverage_display=$(go tool cover -func="$combined_file" | grep total: | awk '{print $3}')

            # Add to summary table
            platform=$(echo "$platform_config" | cut -d'-' -f1,2)
            config=$(echo "$platform_config" | cut -d'-' -f3-)
            echo "| $platform | $config | $coverage_display |" >> "$OVERALL_PR_COMMENT_FILE"
            [ -n "${GITHUB_STEP_SUMMARY:-}" ] && echo "| $platform | $config | $coverage_display |" >> "$GITHUB_STEP_SUMMARY"

            # Add to grand total (remove duplicates by merging)
            grep -h -v '^mode:' "$combined_file" >> "$GRAND_TOTAL"

            # Track for average calculation
            TOTAL_CONFIGS=$((TOTAL_CONFIGS + 1))
            COVERAGE_SUM=$(echo "$COVERAGE_SUM + $coverage_pct" | bc -l)
        else
            platform=$(echo "$platform_config" | cut -d'-' -f1,2)
            config=$(echo "$platform_config" | cut -d'-' -f3-)
            echo "| $platform | $config | ❌ No data |" >> "$OVERALL_PR_COMMENT_FILE"
            [ -n "${GITHUB_STEP_SUMMARY:-}" ] && echo "| $platform | $config | ❌ No data |" >> "$GITHUB_STEP_SUMMARY"
        fi
    done

    echo "" >> "$OVERALL_PR_COMMENT_FILE"
    [ -n "${GITHUB_STEP_SUMMARY:-}" ] && echo "" >> "$GITHUB_STEP_SUMMARY"

    # Calculate and display grand total coverage (deduplicated across all platforms)
    if [ -s "$GRAND_TOTAL" ] && [ $(wc -l < "$GRAND_TOTAL") -gt 1 ]; then
        # Sort and deduplicate coverage lines (same file:line combinations)
        temp_file="temp_coverage.txt"
        echo "mode: atomic" > "$temp_file"
        grep -h -v '^mode:' "$GRAND_TOTAL" | sort -u >> "$temp_file"

        grand_total_pct=$(go tool cover -func="$temp_file" | grep total: | awk '{print $3}')

        # Add to both PR comment and action summary
        echo "### 🏆 Grand Total Coverage: $grand_total_pct" >> "$OVERALL_PR_COMMENT_FILE"
        echo "" >> "$OVERALL_PR_COMMENT_FILE"
        echo "_This represents deduplicated coverage across all platforms and configurations._" >> "$OVERALL_PR_COMMENT_FILE"

        [ -n "${GITHUB_STEP_SUMMARY:-}" ] && {
            echo "### 🏆 Grand Total Coverage: $grand_total_pct" >> "$GITHUB_STEP_SUMMARY"
            echo "" >> "$GITHUB_STEP_SUMMARY"
            echo "_This represents deduplicated coverage across all platforms and configurations._" >> "$GITHUB_STEP_SUMMARY"
        }

        rm -f "$temp_file"
    fi

    # Calculate average coverage across platforms
    if [ $TOTAL_CONFIGS -gt 0 ]; then
        avg_coverage=$(echo "scale=1; $COVERAGE_SUM / $TOTAL_CONFIGS" | bc -l)

        # Add to both PR comment and action summary
        echo "" >> "$OVERALL_PR_COMMENT_FILE"
        echo "Average Coverage Across Platforms: ${avg_coverage}%" >> "$OVERALL_PR_COMMENT_FILE"
        echo "" >> "$OVERALL_PR_COMMENT_FILE"
        echo "Configurations Tested: $TOTAL_CONFIGS" >> "$OVERALL_PR_COMMENT_FILE"

        [ -n "${GITHUB_STEP_SUMMARY:-}" ] && {
            echo "" >> "$GITHUB_STEP_SUMMARY"
            echo "Average Coverage Across Platforms: ${avg_coverage}%" >> "$GITHUB_STEP_SUMMARY"
            echo "" >> "$GITHUB_STEP_SUMMARY"
            echo "Configurations Tested: $TOTAL_CONFIGS" >> "$GITHUB_STEP_SUMMARY"
        }
    fi

    # Add a quick interpretation guide (ONLY to action summary)
    if [ -n "${GITHUB_STEP_SUMMARY:-}" ]; then
        echo "" >> "$GITHUB_STEP_SUMMARY"
        echo "---" >> "$GITHUB_STEP_SUMMARY"
        echo "### 📋 Coverage Report Guide" >> "$GITHUB_STEP_SUMMARY"
        echo "- **Grand Total**: Deduplicated coverage across all platforms (most accurate overall coverage)" >> "$GITHUB_STEP_SUMMARY"
        echo "- **Average Coverage**: Mean coverage percentage across all platform/config combinations" >> "$GITHUB_STEP_SUMMARY"
        echo "- **Per-Platform**: Individual coverage for each OS and cgroup configuration" >> "$GITHUB_STEP_SUMMARY"
    fi

    echo "Overall coverage processing complete!"
    echo "  - PR comment saved to: $OVERALL_PR_COMMENT_FILE"
    echo "  - Grand total coverage: $GRAND_TOTAL"

fi
