package ports

import "testing"

const (
	testCandidateV2   = "candidate-3878d98b1ec776fc72066d589667e27e8d536dda455d861285c169ca5cf24e68"
	testFingerprintV2 = "3878d98b1ec776fc72066d589667e27e8d536dda455d861285c169ca5cf24e68"
	testCandidateV3   = "candidate-a81a418d21f7f867bad1c4c45aee51e8e250efd16b93c0c726bd2bff2ce037f8"
	testFingerprintV3 = "a81a418d21f7f867bad1c4c45aee51e8e250efd16b93c0c726bd2bff2ce037f8"
)

func TestActivationJournalPhaseContract(t *testing.T) {
	t.Parallel()

	valid := []ActivationPhase{
		ActivationPending,
		ActivationQuiesced,
		ActivationRescanning,
		ActivationQuarantining,
		ActivationProjectionRebuild,
		ActivationActive,
		ActivationCandidateDiscarded,
		ActivationLiveMutationStarted,
		ActivationFailed,
		ActivationRollback,
	}
	for _, phase := range valid {
		if !phase.Valid() {
			t.Errorf("phase %q invalid", phase)
		}
	}

	beforeMutation := ActivationRecord{Phase: ActivationRescanning}
	if err := ValidateActivationTransition(beforeMutation, ActivationRollback); err != nil {
		t.Fatalf("pre-mutation rollback rejected: %v", err)
	}
	afterMutation := ActivationRecord{Phase: ActivationLiveMutationStarted, LiveMutationStarted: true}
	if err := ValidateActivationTransition(afterMutation, ActivationRollback); err == nil {
		t.Fatal("post-mutation rollback accepted")
	}
	if err := ValidateActivationTransition(afterMutation, ActivationRescanning); err == nil {
		t.Fatal("post-mutation rescan accepted")
	}

	record := ActivationRecord{SafeError: "scanner unavailable", ResumeCursor: "cursor-1"}
	if err := record.ValidateNoContent(); err != nil {
		t.Fatalf("safe journal record rejected: %v", err)
	}
}

func TestActivationJournalStrictGraph(t *testing.T) {
	t.Parallel()
	base := ActivationRecord{
		Version: 1, ActiveGeneration: "active-v1", CandidateGeneration: testCandidateV2,
		Phase: ActivationPending, RevisionWatermark: 10,
		CandidateRuleFingerprint: testFingerprintV2,
	}
	for _, test := range []struct {
		name string
		next ActivationRecord
		want bool
	}{
		{name: "pending to active", next: ActivationRecord{Version: 2, ActiveGeneration: "active-v1", CandidateGeneration: testCandidateV2, Phase: ActivationActive, RevisionWatermark: 10, CandidateRuleFingerprint: testFingerprintV2}, want: false},
		{name: "pending to quiesced", next: ActivationRecord{Version: 2, ActiveGeneration: "active-v1", CandidateGeneration: testCandidateV2, Phase: ActivationQuiesced, RevisionWatermark: 10, CandidateRuleFingerprint: testFingerprintV2}, want: true},
		{name: "watermark rewind", next: ActivationRecord{Version: 2, ActiveGeneration: "active-v1", CandidateGeneration: testCandidateV2, Phase: ActivationQuiesced, RevisionWatermark: 9, CandidateRuleFingerprint: testFingerprintV2}, want: false},
		{name: "cursor advance", next: ActivationRecord{Version: 2, ActiveGeneration: "active-v1", CandidateGeneration: testCandidateV2, Phase: ActivationQuiesced, RevisionWatermark: 10, CandidateRuleFingerprint: testFingerprintV2, ResumeCursor: "cursor-2"}, want: true},
		{name: "counter rewind", next: ActivationRecord{Version: 2, ActiveGeneration: "active-v1", CandidateGeneration: testCandidateV2, Phase: ActivationQuiesced, RevisionWatermark: 10, CandidateRuleFingerprint: testFingerprintV2, MutationCounts: ActivationMutationCounts{Quarantined: -1}}, want: false},
		{name: "candidate relabel", next: ActivationRecord{Version: 2, ActiveGeneration: "active-v1", CandidateGeneration: testCandidateV3, Phase: ActivationQuiesced, RevisionWatermark: 10, CandidateRuleFingerprint: testFingerprintV2}, want: false},
		{name: "fingerprint relabel", next: ActivationRecord{Version: 2, ActiveGeneration: "active-v1", CandidateGeneration: testCandidateV2, Phase: ActivationQuiesced, RevisionWatermark: 10, CandidateRuleFingerprint: testFingerprintV3}, want: false},
	} {
		t.Run(test.name, func(t *testing.T) {
			got := ValidateActivationRecordTransition(base, test.next) == nil
			if got != test.want {
				t.Fatalf("transition accepted = %v, want %v", got, test.want)
			}
		})
	}

	postLive := base
	postLive.Phase = ActivationLiveMutationStarted
	postLive.LiveMutationStarted = true
	postLive.Version = 2
	if err := ValidateActivationRecordTransition(postLive, ActivationRecord{
		Version: 3, ActiveGeneration: "active-v1", CandidateGeneration: testCandidateV2,
		Phase: ActivationCandidateDiscarded, RevisionWatermark: 10,
		CandidateRuleFingerprint: testFingerprintV2, LiveMutationStarted: true,
	}); err == nil {
		t.Fatal("post-mutation candidate discard accepted")
	}
}

func TestActivationJournalRejectsSafeErrorContentAndRewind(t *testing.T) {
	t.Parallel()
	if err := (ActivationRecord{SafeError: "line\nwith detail"}).ValidateNoContent(); err == nil {
		t.Fatal("newline safe error accepted")
	}
	if err := ValidateActivationRecordTransition(
		ActivationRecord{Phase: ActivationQuiesced, CandidateGeneration: "candidate", ResumeCursor: "cursor-2"},
		ActivationRecord{Phase: ActivationRescanning, CandidateGeneration: "candidate", ResumeCursor: "cursor-1"},
	); err == nil {
		t.Fatal("resume cursor rewind accepted")
	}
}

func TestActivationJournalRejectsOrphanAndUppercaseCandidateIdentity(t *testing.T) {
	if err := (ActivationRecord{CandidateRuleFingerprint: testFingerprintV2}).ValidateNoContent(); err == nil {
		t.Fatal("orphan candidate fingerprint accepted")
	}
	uppercase := "candidate-3878D98B1EC776FC72066D589667E27E8D536DDA455D861285C169CA5CF24E68"
	if err := ValidateCandidateIdentity(uppercase, "3878D98B1EC776FC72066D589667E27E8D536DDA455D861285C169CA5CF24E68"); err == nil {
		t.Fatal("uppercase candidate identity accepted")
	}
}

func TestActivationJournalUpgradeStartAndOrderedPromotion(t *testing.T) {
	t.Parallel()
	active := ActivationRecord{
		Version:             1,
		ActivationEpoch:     "epoch-1",
		ActiveGeneration:    "active-v1",
		Phase:               ActivationActive,
		ComparativeVerified: true,
		RescanVerified:      true,
		MutationVerified:    true,
		ProjectionVerified:  true,
		ReadinessVerified:   true,
	}
	pending := active
	pending.Version = 2
	pending.Phase = ActivationPending
	pending.ActivationEpoch = "epoch-2"
	pending.CandidateGeneration = testCandidateV2
	pending.CandidateRuleFingerprint = testFingerprintV2
	pending.ComparativeVerified = false
	pending.RescanVerified = false
	pending.MutationVerified = false
	pending.ProjectionVerified = false
	pending.ReadinessVerified = false
	if err := ValidateActivationRecordTransition(active, pending); err != nil {
		t.Fatalf("active to pending upgrade start rejected: %v", err)
	}

	bypass := active
	bypass.Version = 2
	bypass.ActivationEpoch = "epoch-2"
	bypass.CandidateGeneration = testCandidateV2
	bypass.CandidateRuleFingerprint = testFingerprintV2
	if err := ValidateActivationRecordTransition(active, bypass); err == nil {
		t.Fatal("same-phase active promotion bypass accepted")
	}

	quiesced := pending
	quiesced.Version++
	quiesced.Phase = ActivationQuiesced
	if err := ValidateActivationRecordTransition(pending, quiesced); err != nil {
		t.Fatalf("pending to quiesced rejected: %v", err)
	}
	rescanning := quiesced
	rescanning.Version++
	rescanning.Phase = ActivationRescanning
	if err := ValidateActivationRecordTransition(quiesced, rescanning); err != nil {
		t.Fatalf("quiesced to rescanning rejected: %v", err)
	}
	quarantining := rescanning
	quarantining.Version++
	quarantining.Phase = ActivationQuarantining
	quarantining.ComparativeVerified = true
	quarantining.RescanVerified = true
	if err := ValidateActivationRecordTransition(rescanning, quarantining); err != nil {
		t.Fatalf("rescanning to quarantining rejected: %v", err)
	}
	projection := quarantining
	projection.Version++
	projection.Phase = ActivationProjectionRebuild
	projection.MutationVerified = true
	if err := ValidateActivationRecordTransition(quarantining, projection); err != nil {
		t.Fatalf("quarantining to projection rebuild rejected: %v", err)
	}
	promoted := projection
	promoted.Version++
	promoted.Phase = ActivationActive
	promoted.ActiveGeneration = testCandidateV2
	promoted.CandidateGeneration = ""
	promoted.CandidateRuleFingerprint = ""
	promoted.ProjectionVerified = true
	promoted.ReadinessVerified = true
	if err := ValidateActivationRecordTransition(projection, promoted); err != nil {
		t.Fatalf("ordered promotion rejected: %v", err)
	}
}

func TestActivationJournalRecoveryAndConsecutiveUpgrades(t *testing.T) {
	t.Parallel()
	active := ActivationRecord{
		Version:             1,
		ActivationEpoch:     "epoch-1",
		ActiveGeneration:    "active-v1",
		Phase:               ActivationActive,
		ComparativeVerified: true,
		RescanVerified:      true,
		MutationVerified:    true,
		ProjectionVerified:  true,
		ReadinessVerified:   true,
	}
	pending := active
	pending.Version = 2
	pending.Phase = ActivationPending
	pending.ActivationEpoch = "epoch-2"
	pending.CandidateGeneration = testCandidateV2
	pending.CandidateRuleFingerprint = testFingerprintV2
	pending.ComparativeVerified = false
	pending.RescanVerified = false
	pending.MutationVerified = false
	pending.ProjectionVerified = false
	pending.ReadinessVerified = false
	failed := pending
	failed.Version++
	failed.Phase = ActivationFailed
	if err := ValidateActivationRecordTransition(pending, failed); err != nil {
		t.Fatalf("pre-live failed state rejected: %v", err)
	}
	if err := ValidateActivationRecordTransition(failed, ActivationRecord{
		Version:                  failed.Version + 1,
		ActiveGeneration:         failed.ActiveGeneration,
		Phase:                    ActivationQuiesced,
		CandidateGeneration:      failed.CandidateGeneration,
		CandidateRuleFingerprint: failed.CandidateRuleFingerprint,
	}); err == nil {
		t.Fatal("failed activation regressed to quiesced")
	}
	discarded := failed
	discarded.Version++
	discarded.Phase = ActivationCandidateDiscarded
	discarded.CandidateGeneration = ""
	discarded.CandidateRuleFingerprint = ""
	if err := ValidateActivationRecordTransition(failed, discarded); err != nil {
		t.Fatalf("pre-live candidate discard rejected: %v", err)
	}
	recovered := discarded
	recovered.Version++
	recovered.Phase = ActivationActive
	recovered.ComparativeVerified = true
	recovered.RescanVerified = true
	recovered.MutationVerified = true
	recovered.ProjectionVerified = true
	recovered.ReadinessVerified = true
	recovered.CandidateDiscardReverified = true
	if err := ValidateActivationRecordTransition(discarded, recovered); err != nil {
		t.Fatalf("pre-live recovery active state rejected: %v", err)
	}
	consecutive := recovered
	consecutive.Version++
	consecutive.Phase = ActivationPending
	consecutive.ActivationEpoch = "epoch-3"
	consecutive.CandidateGeneration = testCandidateV3
	consecutive.CandidateRuleFingerprint = testFingerprintV3
	consecutive.CandidateDiscardReverified = false
	consecutive.ComparativeVerified = false
	consecutive.RescanVerified = false
	consecutive.MutationVerified = false
	consecutive.ProjectionVerified = false
	consecutive.ReadinessVerified = false
	if err := ValidateActivationRecordTransition(recovered, consecutive); err != nil {
		t.Fatalf("consecutive upgrade start rejected: %v", err)
	}

	postQuarantine := pending
	postQuarantine.Version++
	postQuarantine.Phase = ActivationQuiesced
	postRescan := postQuarantine
	postRescan.Version++
	postRescan.Phase = ActivationRescanning
	postQuarantine = postRescan
	postQuarantine.Version++
	postQuarantine.Phase = ActivationQuarantining
	postQuarantine.ComparativeVerified = true
	postQuarantine.RescanVerified = true
	postLive := postQuarantine
	postLive.Version++
	postLive.Phase = ActivationLiveMutationStarted
	postLive.LiveMutationStarted = true
	postLive.ComparativeVerified = true
	postLive.RescanVerified = true
	postLive.MutationVerified = true
	if err := ValidateActivationRecordTransition(postQuarantine, postLive); err != nil {
		t.Fatalf("post-live start rejected: %v", err)
	}
	postProjection := postLive
	postProjection.Version++
	postProjection.Phase = ActivationProjectionRebuild
	if err := ValidateActivationRecordTransition(postLive, postProjection); err != nil {
		t.Fatalf("post-live projection rebuild rejected: %v", err)
	}
	postSuccess := postProjection
	postSuccess.Version++
	postSuccess.Phase = ActivationActive
	postSuccess.ActiveGeneration = testCandidateV2
	postSuccess.CandidateGeneration = ""
	postSuccess.CandidateRuleFingerprint = ""
	postSuccess.ProjectionVerified = true
	postSuccess.ReadinessVerified = true
	if err := ValidateActivationRecordTransition(postProjection, postSuccess); err != nil {
		t.Fatalf("post-live success rejected: %v", err)
	}
	if err := ValidateActivationRecordTransition(postSuccess, postSuccess); err != nil {
		t.Fatalf("verified active historical-live record rejected: %v", err)
	}
	postDiscard := postLive
	postDiscard.Version++
	postDiscard.Phase = ActivationCandidateDiscarded
	postDiscard.CandidateGeneration = ""
	postDiscard.CandidateRuleFingerprint = ""
	if err := ValidateActivationRecordTransition(postLive, postDiscard); err == nil {
		t.Fatal("post-live candidate discard accepted")
	}
}

func TestActivationJournalVerificationGatesCannotRewind(t *testing.T) {
	t.Parallel()
	previous := ActivationRecord{
		Version:                  3,
		ActiveGeneration:         "active-v1",
		CandidateGeneration:      testCandidateV2,
		CandidateRuleFingerprint: testFingerprintV2,
		Phase:                    ActivationProjectionRebuild,
		ComparativeVerified:      true,
		RescanVerified:           true,
		MutationVerified:         true,
	}
	next := previous
	next.Version++
	next.ComparativeVerified = false
	if err := ValidateActivationRecordTransition(previous, next); err == nil {
		t.Fatal("comparative verification rewind accepted")
	}
}

func TestActivationJournalAllowsTwoPostLiveEpochsWithHistoricalAudit(t *testing.T) {
	active := ActivationRecord{
		Version:             1,
		ActivationEpoch:     "epoch-1",
		ActiveGeneration:    "active-v1",
		Phase:               ActivationActive,
		ComparativeVerified: true,
		RescanVerified:      true,
		MutationVerified:    true,
		ProjectionVerified:  true,
		ReadinessVerified:   true,
	}
	complete := func(previous ActivationRecord, epoch, candidate, fingerprint string) ActivationRecord {
		start := previous
		start.Version++
		start.ActivationEpoch = epoch
		start.Phase = ActivationPending
		start.CandidateGeneration = candidate
		start.CandidateRuleFingerprint = fingerprint
		start.LiveMutationStarted = false
		start.LastProcessedID = ""
		start.ResumeCursor = ""
		start.MutationCounts = ActivationMutationCounts{}
		start.ComparativeVerified = false
		start.RescanVerified = false
		start.MutationVerified = false
		start.ProjectionVerified = false
		start.ReadinessVerified = false
		if err := ValidateActivationRecordTransition(previous, start); err != nil {
			t.Fatalf("upgrade start %s rejected: %v", epoch, err)
		}
		quiesced := start
		quiesced.Version++
		quiesced.Phase = ActivationQuiesced
		rescanning := quiesced
		rescanning.Version++
		rescanning.Phase = ActivationRescanning
		quarantine := rescanning
		quarantine.Version++
		quarantine.Phase = ActivationQuarantining
		quarantine.ComparativeVerified = true
		quarantine.RescanVerified = true
		projection := quarantine
		projection.Version++
		projection.Phase = ActivationProjectionRebuild
		projection.MutationVerified = true
		live := projection
		live.Version++
		live.Phase = ActivationLiveMutationStarted
		live.LiveMutationStarted = true
		projectionAfterLive := live
		projectionAfterLive.Version++
		projectionAfterLive.Phase = ActivationProjectionRebuild
		promoted := projectionAfterLive
		promoted.Version++
		promoted.Phase = ActivationActive
		promoted.ActiveGeneration = candidate
		promoted.CandidateGeneration = ""
		promoted.CandidateRuleFingerprint = ""
		promoted.ProjectionVerified = true
		promoted.ReadinessVerified = true
		promoted.HistoricalLiveMutationStarted = true
		if err := ValidateActivationRecordTransition(projectionAfterLive, promoted); err != nil {
			t.Fatalf("promotion %s rejected: %v", epoch, err)
		}
		return promoted
	}
	first := complete(active, "epoch-2", testCandidateV2, testFingerprintV2)
	second := complete(first, "epoch-3", testCandidateV3, testFingerprintV3)
	if !second.HistoricalLiveMutationStarted {
		t.Fatal("second post-live epoch lost historical live audit")
	}
}
