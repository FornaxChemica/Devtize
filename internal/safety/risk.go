package safety

// Risk describes the intended effect of a command knowledge entry.
type Risk string

const (
	RiskReadOnly    Risk = "read_only"
	RiskLocalWrite  Risk = "local_write"
	RiskRemoteWrite Risk = "remote_write"
	RiskDestructive Risk = "destructive"
	RiskPrivileged  Risk = "privileged"
)

func (r Risk) Valid() bool {
	switch r {
	case RiskReadOnly, RiskLocalWrite, RiskRemoteWrite, RiskDestructive, RiskPrivileged:
		return true
	default:
		return false
	}
}

type Decision string

const (
	DecisionAllow   Decision = "allow"
	DecisionConfirm Decision = "confirm"
	DecisionDeny    Decision = "deny"
)

type Evaluation struct {
	Decision Decision `json:"decision"`
	Reason   string   `json:"reason,omitempty"`
}

func Evaluate(risks []Risk) Evaluation {
	for _, risk := range risks {
		switch risk {
		case RiskDestructive:
			return Evaluation{Decision: DecisionDeny, Reason: "destructive operations are not available in Phase B"}
		case RiskPrivileged:
			return Evaluation{Decision: DecisionDeny, Reason: "privileged operations are not available in Phase B"}
		}
	}
	for _, risk := range risks {
		if risk == RiskLocalWrite || risk == RiskRemoteWrite {
			return Evaluation{Decision: DecisionConfirm}
		}
	}
	return Evaluation{Decision: DecisionAllow}
}

func ContainsRisk(risks []Risk, target Risk) bool {
	for _, risk := range risks {
		if risk == target {
			return true
		}
	}
	return false
}
