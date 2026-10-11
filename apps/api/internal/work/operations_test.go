package work

import (
	"fmt"
	"testing"
	"time"
)

func TestWorkConcurrencyIndependentVerificationAndOutputContract(t *testing.T) {
	p, err := New("fixture", 1, Request{Name: "Bounded plan"})
	if err != nil {
		t.Fatal(err)
	}
	p.Members = append(p.Members, Member{2, "contributor", "active"})
	now := time.Now()
	next := 0
	newID := func() string { next++; return fmt.Sprint(next) }
	apply := func(actor int64, r Request, want int) any {
		t.Helper()
		result, e := p.Apply(Actor{UserID: actor}, r, now, newID)
		if want == 0 {
			if e != nil {
				t.Fatal(e)
			}
		} else {
			f, ok := e.(*Failure)
			if !ok || f.Status != want {
				t.Fatalf("%s: got %v want %d", r.Action, e, want)
			}
		}
		return result
	}
	specs := []AssignmentSpec{}
	for i := range 4 {
		specs = append(specs, AssignmentSpec{ID: fmt.Sprint(i), Title: "Work", Instructions: "Bounded", Assignee: 1, ExpectedOutputs: []string{"result.txt"}})
	}
	specs = append(specs, AssignmentSpec{ID: "verify", Title: "Verify", Instructions: "Must be independent", Assignee: 1, Kind: "verification", Dependencies: []string{"0"}})
	apply(1, Request{Action: "plan", Assignments: specs}, 0)
	attempts := []Attempt{}
	for i := range 3 {
		attempts = append(attempts, apply(1, Request{Action: "claim", AssignmentID: fmt.Sprint(i)}, 0).(Attempt))
	}
	apply(1, Request{Action: "claim", AssignmentID: "3"}, 409)
	apply(1, Request{Action: "submit", AssignmentID: "0", AttemptID: attempts[0].ID}, 409)
	p.Artifacts = append(p.Artifacts, Artifact{ID: "output", Name: "result.txt", OwnerID: 1, AssignmentID: "0", AttemptID: attempts[0].ID})
	cp := apply(1, Request{Action: "submit", AssignmentID: "0", AttemptID: attempts[0].ID, Outputs: []string{"output"}}, 0).(Contribution)
	apply(1, Request{Action: "accept", AssignmentID: "0", ContributionID: cp.ID}, 0)
	apply(1, Request{Action: "claim", AssignmentID: "verify"}, 403)
	now = now.Add(301 * time.Second)
	apply(1, Request{Action: "renew", AssignmentID: "1", AttemptID: attempts[1].ID}, 409)
	apply(1, Request{Action: "claim", AssignmentID: "3"}, 0)
}
