package curation

import (
	"context"
	"time"
)

type JobState string

const (
	JobPending   JobState = "pending"
	JobRunning   JobState = "running"
	JobRetryWait JobState = "retry_wait"
	JobComplete  JobState = "complete"
	JobTerminal  JobState = "terminal"
)

func (state JobState) Valid() bool {
	switch state {
	case JobPending, JobRunning, JobRetryWait, JobComplete, JobTerminal:
		return true
	default:
		return false
	}
}

func PriorityForReason(reason Reason) int {
	switch reason {
	case ReasonPreCompact:
		return 3
	case ReasonSessionEnd:
		return 2
	case ReasonPeriodic:
		return 1
	default:
		return 0
	}
}

type Job struct {
	ID                      string
	WorkspaceID             string
	Reason                  Reason
	Reasons                 []Reason
	Priority                int
	SessionDigest           []byte
	SourceWatermark         int64
	ThreadLocatorCiphertext []byte
	SnapshotCiphertext      []byte
	State                   JobState
	AttemptCount            int
	NextAttemptAt           *time.Time
	ExpiresAt               time.Time
	ProviderName            string
	SafeErrorClass          ErrorClass
	CreatedAt               time.Time
	UpdatedAt               time.Time
}

type SessionCounter struct {
	SessionDigest []byte
	PromptCount   int64
	LastWatermark int64
	ExpiresAt     time.Time
}

type JobStore interface {
	Enqueue(context.Context, Job) (Job, bool, error)
	ClaimNext(context.Context, time.Time) (Job, bool, error)
	Retry(context.Context, string, int, time.Time, ErrorClass) error
	Finish(context.Context, string, JobState, ErrorClass) error
	PurgeExpired(context.Context, time.Time) (int64, error)
	IncrementPrompt(context.Context, SessionCounter) (SessionCounter, error)
	EndSession(context.Context, []byte) error
}
