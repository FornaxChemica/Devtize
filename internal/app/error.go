package app

import "fmt"

type ErrorCode string

const (
	CodeInvalidUsage           ErrorCode = "INVALID_USAGE"
	CodeConfigInvalid          ErrorCode = "CONFIG_INVALID"
	CodeToolNotFound           ErrorCode = "TOOL_NOT_FOUND"
	CodeToolVersionUnsupported ErrorCode = "TOOL_VERSION_UNSUPPORTED"
	CodeProjectNotFound        ErrorCode = "PROJECT_NOT_FOUND"
	CodeCapabilityNotFound     ErrorCode = "CAPABILITY_NOT_FOUND"
	CodeAuthRequired           ErrorCode = "AUTH_REQUIRED"
	CodePlanInvalid            ErrorCode = "PLAN_INVALID"
	CodePolicyDenied           ErrorCode = "POLICY_DENIED"
	CodeConfirmationDeclined   ErrorCode = "CONFIRMATION_DECLINED"
	CodePreconditionFailed     ErrorCode = "PRECONDITION_FAILED"
	CodeProcessTimeout         ErrorCode = "PROCESS_TIMEOUT"
	CodeProcessFailed          ErrorCode = "PROCESS_FAILED"
	CodePartialExecution       ErrorCode = "PARTIAL_EXECUTION"
	CodeAIUnavailable          ErrorCode = "AI_UNAVAILABLE"
	CodeAIOutputInvalid        ErrorCode = "AI_OUTPUT_INVALID"
	CodeHistoryWriteFailed     ErrorCode = "HISTORY_WRITE_FAILED"
	CodePostconditionFailed    ErrorCode = "POSTCONDITION_FAILED"
)

type Error struct {
	Code      ErrorCode `json:"code"`
	Message   string    `json:"message"`
	Hint      string    `json:"hint,omitempty"`
	Provider  string    `json:"provider,omitempty"`
	Retryable bool      `json:"retryable"`
	Cause     error     `json:"-"`
}

func (e *Error) Error() string {
	if e.Message != "" {
		return e.Message
	}
	return string(e.Code)
}

func (e *Error) Unwrap() error { return e.Cause }

func Wrap(code ErrorCode, message string, cause error) *Error {
	return &Error{Code: code, Message: message, Cause: cause}
}

func (e *Error) SafeText() string {
	if e.Hint == "" {
		return fmt.Sprintf("%s: %s", e.Code, e.Message)
	}
	return fmt.Sprintf("%s: %s\nHint: %s", e.Code, e.Message, e.Hint)
}
