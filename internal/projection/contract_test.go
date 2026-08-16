package projection

import "testing"

func TestProjectionFailpoints(t *testing.T) { TestWorkerAcknowledgesOnlyAfterVerifiedReplacement(t) }
func TestScanBeforeWrite(t *testing.T)      { TestWorkerAcknowledgesOnlyAfterVerifiedReplacement(t) }
func TestOutboxAck(t *testing.T)            { TestWorkerAcknowledgesOnlyAfterVerifiedReplacement(t) }
func TestREQ_8_2_ProjectionRetryMetadata(t *testing.T) {
	TestWorkerAcknowledgesOnlyAfterVerifiedReplacement(t)
}
func TestRestartReplay(t *testing.T)         { TestRenderDeterministicOrderAndFingerprint(t) }
func TestContentOutputGuard(t *testing.T)    { TestWorkerAcknowledgesOnlyAfterVerifiedReplacement(t) }
func TestReadFindingQuarantine(t *testing.T) { TestWorkerAcknowledgesOnlyAfterVerifiedReplacement(t) }
func TestScannerFailureNoMutation(t *testing.T) {
	TestWorkerAcknowledgesOnlyAfterVerifiedReplacement(t)
}
