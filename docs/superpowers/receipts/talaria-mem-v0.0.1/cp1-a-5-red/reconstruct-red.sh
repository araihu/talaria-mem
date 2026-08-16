#!/bin/sh
set -eu

TASK=cp1-a-5
ROOT=$(CDPATH= cd -- "$(dirname -- "$0")/../../../../.." && pwd)
TMP=$(mktemp -d "${TMPDIR:-/tmp}/talaria-mem-cp1-a-5-red.XXXXXX")
trap 'rm -rf "$TMP"' EXIT

archive_source() {
	commit=$1
	tree=$2
	destination=$3
	test "$(git -C "$ROOT" rev-parse "$commit^{tree}")" = "$tree"
	mkdir -p "$destination"
	git -C "$ROOT" archive --format=tar "$commit" | tar -xf - -C "$destination"
}

copy_overlay() {
	root=$1
	source=$2
	target=$3
	want=$4
	test "$(shasum -a 256 "$ROOT/$source" | awk '{print $1}')" = "$want"
	mkdir -p "$(dirname -- "$root/$target")"
	cp "$ROOT/$source" "$root/$target"
}

SOURCE_T1_T3_COMMIT=e03399430df655706612c33a83cc8a3905b9e775
SOURCE_T1_T3_TREE=ca1070ecc440aa81b2520d5b8b3bfce91f27c4c9
SOURCE_T2_T4_COMMIT=069281c7ec57b3b4ba51e3a51f4059dfa98b4925
SOURCE_T2_T4_TREE=2a7015f474098dde3ea0b393a365f52b3b0085e1
archive_source "$SOURCE_T1_T3_COMMIT" "$SOURCE_T1_T3_TREE" "$TMP/t1"
archive_source "$SOURCE_T1_T3_COMMIT" "$SOURCE_T1_T3_TREE" "$TMP/t3-explicit"
archive_source "$SOURCE_T1_T3_COMMIT" "$SOURCE_T1_T3_TREE" "$TMP/t3-schema"
archive_source "$SOURCE_T1_T3_COMMIT" "$SOURCE_T1_T3_TREE" "$TMP/t3-migration"
archive_source "$SOURCE_T2_T4_COMMIT" "$SOURCE_T2_T4_TREE" "$TMP/t2"
archive_source "$SOURCE_T2_T4_COMMIT" "$SOURCE_T2_T4_TREE" "$TMP/t4"

copy_overlay "$TMP/t1" testdata/cp1-a-5/red-overlay/cmd/talaria-mem/cp1_a5_bootstrap_red_test.go \
	cmd/talaria-mem/cp1_a5_bootstrap_red_test.go \
	4ed8213244a6562a44e5cb442d81af131196df73a9ef3ddf22392ef29173b3df
copy_overlay "$TMP/t1" testdata/bootstrap/cp1-a-5-provider-free.json \
	testdata/bootstrap/cp1-a-5-provider-free.json \
	97f3d89a94156a9b898ecf2703b5d8bee11a89c25e5ab3546e444cb28af632ae

copy_overlay "$TMP/t3-explicit" testdata/cp1-a-5/red-overlay/internal/adapters/sqlite/cp1_a5_explicit_dependencies_test.go \
	internal/adapters/sqlite/cp1_a5_explicit_dependencies_test.go \
	cc40c9aace5a195b68b9e40a62034dcd76150dd1959fc1fdc2f3cae351cc43de
copy_overlay "$TMP/t3-schema" testdata/cp1-a-5/red-overlay/internal/adapters/sqlite/cp1_a5_schema_red_test.go \
	internal/adapters/sqlite/cp1_a5_schema_red_test.go \
	f39476440a1c87fbf3dc509322f8f8ab2dea30184d63efaf0d493cc201f38932
copy_overlay "$TMP/t3-migration" testdata/cp1-a-5/red-overlay/internal/adapters/sqlite/cp1_a5_migration_red_test.go \
	internal/adapters/sqlite/cp1_a5_migration_red_test.go \
	322df50c88be681964c077fbbeefdb1a37a490cafae8f5157c6be489a18f603c

copy_overlay "$TMP/t2" testdata/cp1-a-5/red-overlay/internal/ports/cp1_a5_activation_red_test.go \
	internal/ports/cp1_a5_activation_red_test.go \
	c4b428666b161aa390381c75af1ad1ef7e1b93f6d751360549c78413cc2bce02
copy_overlay "$TMP/t4" testdata/cp1-a-5/red-overlay/internal/scanner/cp1_a5_metadata_red_test.go \
	internal/scanner/cp1_a5_metadata_red_test.go \
	08e05151705fac10b1255b82474ec0ff3c2e21451ce8bedab922a03b2e0b3273
copy_overlay "$TMP/t4" testdata/secrets/cp1-a-4-metadata-canary.json \
	testdata/secrets/cp1-a-4-metadata-canary.json \
	f939c7ff2467085cb326b21303ba083411abf0dd1659615992117e3f4cedd805

run_red() {
	scope=$1
	root=$2
	shift 2
	set +e
	(
		cd "$root"
		"$@"
	)
	status=$?
	set -e
	test "$status" -eq 1
	printf 'task=%s scope=%s phase=RED expected_exit=1 actual_exit=%s\n' "$TASK" "$scope" "$status"
}

run_red T1-bootstrap "$TMP/t1" go test ./cmd/talaria-mem ./internal/testutil -count=1
run_red T3-explicit-filesystem "$TMP/t3-explicit" go test ./internal/adapters/sqlite -run '^TestSQLiteUsesExplicitFilesystemPortInjection$' -count=1
run_red T3-schema-contract "$TMP/t3-schema" sh -c "go test ./internal/adapters/sqlite -run 'TestSchema|TestTransaction|TestFTS|TestFTSTokenizer|TestUnicodeDiacritic|TestSQLiteFull|TestActivationJournal' -count=1"
run_red T3-migration-journal "$TMP/t3-migration" go test ./internal/adapters/sqlite -run 'TestMigrationRoundTrip' -count=1
run_red T2-domain "$TMP/t2" sh -c "go test ./internal/domain ./internal/ports -run 'TestResolutionState|TestLimits|TestKeyDeriver|TestNormalizeV1|TestManagedFileStore|TestActivationJournal|TestFTSConfig|TestSQLiteFull' -count=1"
run_red T4-scanner "$TMP/t4" env TALARIA_SCANNER_NETWORK=disabled go test ./internal/scanner -run 'TestBoundary|TestRuleUpgrade|TestNoEgress' -count=1
