package safety

import "testing"

func TestRiskValuesRemainStable(t *testing.T) {
	want := []Risk{"read_only", "local_write", "remote_write", "destructive", "privileged"}
	got := []Risk{RiskReadOnly, RiskLocalWrite, RiskRemoteWrite, RiskDestructive, RiskPrivileged}
	for index := range want {
		if got[index] != want[index] || !got[index].Valid() {
			t.Fatalf("risk %d = %q, want %q and valid", index, got[index], want[index])
		}
	}
	if Risk("other").Valid() {
		t.Fatal("unknown risk reported as valid")
	}
}
