#!/usr/bin/env bash
# 跑测试 N 轮，统计每个 test 的 fail 次数，输出 flaky report
# 兼容 bash 3.2（macOS 系统自带版本）
set -u

ROUNDS=${ROUNDS:-20}
PACKAGES=${PACKAGES:-./...}

TMPFILE=$(mktemp /tmp/flaky_failures.XXXXXX)
trap 'rm -f "$TMPFILE"' EXIT

for i in $(seq 1 "$ROUNDS"); do
    echo "Round $i/$ROUNDS"
    output=$(go test $PACKAGES -race -count=1 -timeout=120s 2>&1 || true)
    while IFS= read -r line; do
        # match `--- FAIL: TestFoo (0.01s)` lines
        if echo "$line" | grep -qE '^--- FAIL: [A-Za-z0-9_/]+'; then
            test=$(echo "$line" | sed -E 's/^--- FAIL: ([A-Za-z0-9_/]+).*/\1/')
            echo "$test" >> "$TMPFILE"
        fi
        # track DATA RACE occurrences
        if echo "$line" | grep -q 'DATA RACE'; then
            echo "__DATA_RACE__" >> "$TMPFILE"
        fi
    done <<< "$output"
done

echo ""
echo "=== Flaky Report (after $ROUNDS rounds, -race) ==="
if [ ! -s "$TMPFILE" ]; then
    echo "  No failures detected — all tests passed $ROUNDS/$ROUNDS"
else
    sort "$TMPFILE" | uniq -c | sort -rn | while read count test; do
        echo "  $count/$ROUNDS  $test"
    done
fi
