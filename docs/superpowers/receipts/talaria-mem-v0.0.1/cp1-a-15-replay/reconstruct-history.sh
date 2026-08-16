#!/bin/sh
set -eu

BASE=c087dc98b539bda96727324247eef3432b60f6dd
T1_RED=b54ccb49b1640340be84e7d9579031e911a2c139
T1_RED_EVIDENCE=588593e255905382fc47f958ae8a1abeb8500966
T1_GREEN=51e2a727ff214c89ddb9fa6e27ece3d7f4420b5f
T1_GREEN_EVIDENCE=b1dc7a54c9c1f73a338c7b605df6613478c4c796
T2_RED=2bd532021a764e7d59fc579ace44bd2b8f5de354
T2_RED_EVIDENCE=c406af74a31a6f0e80ba222c61a83b709aeab6a2
T2_GREEN=9ea5f7f3b98976d7e44fb57d787fa067da96b50e
T2_GREEN_EVIDENCE=a99e5bd66643372063209fce9aafc4a67e27e61c
T3_RED=5ca0a05bcde7b69075cb037376b862c3a8d4c7bb
T3_RED_EVIDENCE=0279c17c4814b856656c70f90eaa1bb4f38d9ca7
T3_GREEN=dbcfb851ff7d0ccc1eb9645d08ca22d3be803681
T3_GREEN_EVIDENCE=04d5d0206e7ca0ec281f64b53187c5267e73a817
T4_RED=26594bc2caf5dd16433f16e372f8485c47b91443
T4_RED_EVIDENCE=4581da55c46d124f4c897f3bef2570661fcb18a3
T4_GREEN=c2f7c96fec6fa93bb68b8ed044d801ec689a8b74
T4_GREEN_EVIDENCE=745f2ab5aa27c2597854cf8870e23c05641a4918
MERGE=60d3b3a4384d0fce3b9a8c4a210073dd670bb125

repo=$(git rev-parse --show-toplevel)
tmp=$(mktemp -d "${TMPDIR:-/tmp}/talaria-mem-cp1-a15-replay.XXXXXX")
trap 'rm -rf "$tmp"' EXIT INT TERM

die() {
	printf '%s\n' "replay: $*" >&2
	exit 1
}

tree_for() {
	git rev-parse "$1^{tree}"
}

parent_is() {
	commit=$1
	parent=$2
	actual=$(git rev-parse "$commit^1")
	[ "$actual" = "$parent" ] || die "$commit parent=$actual want=$parent"
}

parent_is "$T1_RED" "$BASE"
parent_is "$T1_RED_EVIDENCE" "$T1_RED"
parent_is "$T1_GREEN" "$T1_RED_EVIDENCE"
parent_is "$T1_GREEN_EVIDENCE" "$T1_GREEN"
parent_is "$T2_RED" "$T1_GREEN_EVIDENCE"
parent_is "$T2_RED_EVIDENCE" "$T2_RED"
parent_is "$T2_GREEN" "$T2_RED_EVIDENCE"
parent_is "$T2_GREEN_EVIDENCE" "$T2_GREEN"
parent_is "$T3_RED" "$T2_GREEN_EVIDENCE"
parent_is "$T3_RED_EVIDENCE" "$T3_RED"
parent_is "$T3_GREEN" "$T3_RED_EVIDENCE"
parent_is "$T3_GREEN_EVIDENCE" "$T3_GREEN"
parent_is "$T4_RED" "$T2_GREEN_EVIDENCE"
parent_is "$T4_RED_EVIDENCE" "$T4_RED"
parent_is "$T4_GREEN" "$T4_RED_EVIDENCE"
parent_is "$T4_GREEN_EVIDENCE" "$T4_GREEN"
[ "$(git rev-parse "$MERGE^1")" = "$T3_GREEN_EVIDENCE" ] || die "merge first parent is not T3 green evidence"
[ "$(git rev-parse "$MERGE^2")" = "$T4_GREEN_EVIDENCE" ] || die "merge second parent is not T4 green evidence"

run_case() {
	label=$1
	commit=$2
	expected=$3
	command=$4
	dir="$tmp/$label"
	mkdir -p "$dir"
	git archive "$commit" | tar -x -C "$dir"
	set +e
	(
		cd "$dir" || exit 1
		GOWORK=off GOCACHE="$tmp/go-cache-$label" sh -c "$command"
	) >"$tmp/$label.stdout" 2>"$tmp/$label.stderr"
	actual=$?
	set -e
	[ "$actual" = "$expected" ] || die "$label exit=$actual want=$expected"
	printf 'replay=%s commit=%s expected_exit=%s actual_exit=%s stdout_sha256=%s stderr_sha256=%s\n' \
		"$label" "$commit" "$expected" "$actual" \
		"$(shasum -a 256 "$tmp/$label.stdout" | awk '{print $1}')" \
		"$(shasum -a 256 "$tmp/$label.stderr" | awk '{print $1}')"
}

run_case t1-red "$T1_RED" 1 "go test ./cmd/talaria-mem ./internal/testutil -count=1"
run_case t1-green "$T1_GREEN" 0 "go test ./cmd/talaria-mem ./internal/testutil -count=1 && go vet ./cmd/talaria-mem ./internal/testutil"
run_case t2-red "$T2_RED" 1 "go test ./internal/domain ./internal/ports -run 'TestResolutionState|TestLimits|TestKeyDeriver|TestNormalizeV1|TestManagedFileStore|TestActivationJournal|TestFTSConfig|TestSQLiteFull' -count=1"
run_case t2-green "$T2_GREEN" 0 "go test ./internal/domain ./internal/ports -run 'TestResolutionState|TestLimits|TestKeyDeriver|TestNormalizeV1|TestManagedFileStore|TestActivationJournal|TestFTSConfig|TestSQLiteFull' -count=1 && go vet ./internal/domain ./internal/ports"
run_case t3-schema-red "$T3_RED" 1 "go test ./internal/adapters/sqlite -run 'TestSchema|TestTransaction|TestFTS|TestFTSTokenizer|TestUnicodeDiacritic|TestSQLiteFull|TestActivationJournal' -count=1"
run_case t3-migration-red "$T3_RED" 1 "go test ./internal/adapters/sqlite -run 'TestMigrationRoundTrip' -count=1"
run_case t3-green "$T3_GREEN" 0 "go test ./internal/adapters/sqlite -run 'TestSchema|TestTransaction|TestFTS|TestFTSTokenizer|TestUnicodeDiacritic|TestSQLiteFull|TestActivationJournal' -count=1 && go test -race ./internal/adapters/sqlite"
run_case t4-red "$T4_RED" 1 "go test ./internal/scanner -run 'TestBoundary|TestRuleUpgrade|TestNoEgress' -count=1"
run_case t4-green "$T4_GREEN" 0 "TALARIA_SCANNER_NETWORK=disabled go test ./internal/scanner -run 'TestBoundary|TestRuleUpgrade|TestNoEgress' -count=1"

printf 'replay=history status=0 base=%s merge=%s\n' "$BASE" "$MERGE"
