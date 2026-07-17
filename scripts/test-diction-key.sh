#!/usr/bin/env bash
#
# Pure-bash test harness for diction-key. Runs WITHOUT root, on a temporary
# file — it never touches the real AX42 tokens file. Requires GNU coreutils
# (stat -c) and openssl, both present on Debian.
#
set -uo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
CLI="$SCRIPT_DIR/diction-key"

tmpdir="$(mktemp -d)"
trap 'rm -rf "$tmpdir"' EXIT
# Point at a not-yet-existing file inside a fresh dir to exercise auto-create.
export DICTION_TOKENS_FILE="$tmpdir/config/tokens.txt"

pass=0
fail=0
ok() {
	printf 'ok   - %s\n' "$1"
	pass=$((pass + 1))
}
no() {
	printf 'FAIL - %s\n' "$1"
	fail=$((fail + 1))
}
# check <desc> <bash-condition-string>
check() { if eval "$2"; then ok "$1"; else no "$1"; fi; }
# expect_fail <desc> <command...> — passes if the command exits non-zero.
expect_fail() {
	local desc="$1"
	shift
	if "$@" >/dev/null 2>&1; then no "$desc"; else ok "$desc"; fi
}

# --- create ---------------------------------------------------------------
out="$("$CLI" create alice)"
tok_alice="$("$CLI" show alice)"
check "create prints a 64-hex token" '[[ "$out" =~ [0-9a-f]{64} ]]'
check "file has a well-formed token:alice line" 'grep -qE "^[0-9a-f]{64}:alice\$" "$DICTION_TOKENS_FILE"'
check "show returns a 64-hex token" '[[ "$tok_alice" =~ ^[0-9a-f]{64}$ ]]'

inode0="$(stat -c %i "$DICTION_TOKENS_FILE")"

# --- create rejects duplicates and invalid names --------------------------
expect_fail "create rejects a duplicate name" "$CLI" create alice
expect_fail "create rejects name with ':'" "$CLI" create "bad:name"
expect_fail "create rejects name with space" "$CLI" create "bad name"
expect_fail "create rejects name with slash" "$CLI" create "bad/name"
expect_fail "create rejects name with '\$'" "$CLI" create 'ba$d'
expect_fail "create rejects empty name" "$CLI" create ""

# invalid-name attempts must not have modified the file
check "file still has exactly one entry after failed creates" '[[ "$(grep -cE ":[A-Za-z0-9_-]+\$" "$DICTION_TOKENS_FILE")" == "1" ]]'

# --- list masks the token -------------------------------------------------
list_out="$("$CLI" list)"
check "list shows the name" 'grep -q "alice" <<<"$list_out"'
check "list never prints the full token" '! grep -q "$tok_alice" <<<"$list_out"'
check "list shows the masked ellipsis" 'grep -q "…" <<<"$list_out"'
check "list shows the token prefix (first 8)" 'grep -q "${tok_alice:0:8}" <<<"$list_out"'

# --- show -----------------------------------------------------------------
check "show returns the exact created token" '[[ "$("$CLI" show alice)" == "$tok_alice" ]]'
expect_fail "show fails for a missing name" "$CLI" show ghost

# --- second create: inode must be unchanged (append in place) -------------
"$CLI" create bob >/dev/null
tok_bob="$("$CLI" show bob)"
inode1="$(stat -c %i "$DICTION_TOKENS_FILE")"
check "inode unchanged after create" '[[ "$inode0" == "$inode1" ]]'

# --- delete removes the right line, keeps the others, keeps the inode -----
"$CLI" delete alice >/dev/null
check "delete removed alice" '! grep -qE ":alice\$" "$DICTION_TOKENS_FILE"'
check "delete kept bob" 'grep -qE "^[0-9a-f]{64}:bob\$" "$DICTION_TOKENS_FILE"'
check "bob token intact after delete" '[[ "$("$CLI" show bob)" == "$tok_bob" ]]'
inode2="$(stat -c %i "$DICTION_TOKENS_FILE")"
check "inode unchanged after delete" '[[ "$inode0" == "$inode2" ]]'

expect_fail "delete fails for a missing name" "$CLI" delete ghost

# --- summary --------------------------------------------------------------
printf '\n%d passed, %d failed\n' "$pass" "$fail"
[[ "$fail" -eq 0 ]]
