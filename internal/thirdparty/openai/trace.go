package openai

import "context"

type GenerationModelExecutionTrace struct {
	PrimaryModel         string `json:"primaryModel"`
	PrimaryUsedModel     string `json:"primaryUsedModel"`
	PrimaryFailed        bool   `json:"primaryFailed"`
	PrimaryFailureReason string `json:"primaryFailureReason"`

	BackupModel         string `json:"backupModel"`
	BackupUsed          bool   `json:"backupUsed"`
	BackupUsedModel     string `json:"backupUsedModel"`
	BackupSucceeded     bool   `json:"backupSucceeded"`
	BackupFailureReason string `json:"backupFailureReason"`
}

type CheckModelExecutionTrace struct {
	Model               string `json:"model"`
	BackupModel         string `json:"backupModel"`
	BackupUsed          bool   `json:"backupUsed"`
	BackupUsedModel     string `json:"backupUsedModel"`
	BackupSucceeded     bool   `json:"backupSucceeded"`
	BackupFailureReason string `json:"backupFailureReason"`
	Attempted           bool   `json:"attempted"`
	Succeeded           bool   `json:"succeeded"`
	Failed              bool   `json:"failed"`
	FailureReason       string `json:"failureReason"`
}

type RecapExecutionTrace struct {
	Generation GenerationModelExecutionTrace `json:"generation"`
	Check      CheckModelExecutionTrace      `json:"check"`
}

type CondensedExecutionTrace struct {
	Generation GenerationModelExecutionTrace `json:"generation"`
}

type recapExecutionTraceContextKey struct{}
type condensedExecutionTraceContextKey struct{}

func WithRecapExecutionTrace(ctx context.Context, trace *RecapExecutionTrace) context.Context {
	return context.WithValue(ctx, recapExecutionTraceContextKey{}, trace)
}

func WithCondensedExecutionTrace(ctx context.Context, trace *CondensedExecutionTrace) context.Context {
	return context.WithValue(ctx, condensedExecutionTraceContextKey{}, trace)
}

func recapExecutionTraceFromContext(ctx context.Context) *RecapExecutionTrace {
	trace, _ := ctx.Value(recapExecutionTraceContextKey{}).(*RecapExecutionTrace)
	return trace
}

func condensedExecutionTraceFromContext(ctx context.Context) *CondensedExecutionTrace {
	trace, _ := ctx.Value(condensedExecutionTraceContextKey{}).(*CondensedExecutionTrace)
	return trace
}
