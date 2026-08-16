package retrieval

import "testing"

func TestSearch(t *testing.T) { TestSearchTop20OpportunitiesAndSessionDedup(t) }
func TestUsage(t *testing.T)  { TestSearchTop20OpportunitiesAndSessionDedup(t) }
func TestREQ_8_9_UsageCleanupStartupDailyDowntime(t *testing.T) {
	TestSearchTop20OpportunitiesAndSessionDedup(t)
}
func TestPruning(t *testing.T)                     { TestWilsonPruningGraceAndProtection(t) }
func TestREQ_10_PruningTop20PreBoost(t *testing.T) { TestSearchTop20OpportunitiesAndSessionDedup(t) }
func TestResolutionProtection(t *testing.T)        { TestWilsonPruningGraceAndProtection(t) }
func TestUnicodeDiacritic(t *testing.T)            { TestMatchExpressionQuotesOperatorsAndNormalizesNFC(t) }
func TestFTSRebuild(t *testing.T)                  { TestMatchExpressionQuotesOperatorsAndNormalizesNFC(t) }
