package httpadapter

const (
	HealthPath           = "/healthz"
	ReadinessPath        = "/readyz"
	SessionStartPath     = "/control/v1/session-start"
	UserPromptSubmitPath = "/control/v1/user-prompt-submit"
	PreCompactPath       = "/control/v1/pre-compact"
	SessionEndPath       = "/control/v1/session-end"
)
