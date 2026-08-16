package workspace

import "testing"

func TestInference(t *testing.T) { TestNormalizeGitRemoteAndInferenceWarning(t) }
func TestMerge(t *testing.T)     { TestMergeExactDuplicateConflictReviewAndReceipt(t) }
func TestREQ_6_1_MergeNonIdenticalReview(t *testing.T) {
	TestMergeExactDuplicateConflictReviewAndReceipt(t)
}
func TestReceipt(t *testing.T) { TestMergeReceiptExpiryAndCollision(t) }
