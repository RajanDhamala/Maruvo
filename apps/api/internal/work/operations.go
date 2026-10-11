package work

import (
	"maps"
	"slices"
	"strings"
	"time"
	"unicode"
)

// Request is also the public JSON file contract for work commands.
type Request struct {
	OperationID          string           `json:"operation_id"`
	Action               string           `json:"action,omitempty"`
	Name                 string           `json:"name,omitempty"`
	PostID               int64            `json:"post_id,omitempty"`
	Concurrency          int              `json:"concurrency,omitempty"`
	AssignmentID         string           `json:"assignment_id,omitempty"`
	UserID               int64            `json:"user_id,omitempty"`
	Role                 string           `json:"role,omitempty"`
	Assignments          []AssignmentSpec `json:"assignments,omitempty"`
	AttemptID            string           `json:"attempt_id,omitempty"`
	LeaseSeconds         int              `json:"lease_seconds,omitempty"`
	ContributionID       string           `json:"contribution_id,omitempty"`
	Contributions        []string         `json:"contributions,omitempty"`
	Inputs               []string         `json:"inputs,omitempty"`
	Outputs              []string         `json:"outputs,omitempty"`
	Evidence             []Evidence       `json:"evidence,omitempty"`
	BaseRevision         int64            `json:"base_revision"`
	Note                 string           `json:"note,omitempty"`
	Reason               string           `json:"reason,omitempty"`
	NextAction           string           `json:"next_action,omitempty"`
	Mode                 string           `json:"mode,omitempty"`
	ArtifactName         string           `json:"artifact_name,omitempty"`
	Content              []byte           `json:"content,omitempty"`
	SourceFile           string           `json:"source_file,omitempty"`
	CostLamports         int64            `json:"cost_lamports,omitempty"`
	EndTime              time.Time        `json:"end_time,omitempty"`
	DeliverBy            time.Time        `json:"deliver_by,omitempty"`
	FundingWindowSeconds int64            `json:"funding_window_seconds,omitempty"`
	ReviewWindowSeconds  int64            `json:"review_window_seconds,omitempty"`
	IntegrationID        string           `json:"integration_id,omitempty"`
	SubmissionVersion    int64            `json:"submission_version,omitempty"`
	Permissions          []string         `json:"permissions,omitempty"`
	AllowedMembers       []int64          `json:"allowed_members,omitempty"`
	ExpiresInSeconds     int64            `json:"expires_in_seconds,omitempty"`
	GrantID              string           `json:"grant_id,omitempty"`
	Usage                map[string]int64 `json:"usage,omitempty"`
}
type AssignmentSpec struct {
	ID              string   `json:"id"`
	Title           string   `json:"title"`
	Instructions    string   `json:"instructions"`
	Kind            string   `json:"kind"`
	Assignee        int64    `json:"assignee"`
	Dependencies    []string `json:"dependencies"`
	Inputs          []string `json:"inputs"`
	ExpectedOutputs []string `json:"expected_outputs"`
}

func ValidID(id string) bool {
	return len(id) > 0 && len(id) <= 128 && strings.IndexFunc(id, func(r rune) bool {
		return !(r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || r == '_' || r == '-')
	}) < 0
}
func ValidFilename(name string) bool {
	return len(name) > 0 && len(name) <= 180 && name != "." && name != ".." && strings.TrimSpace(name) == name && !strings.ContainsAny(name, "/\\") && strings.IndexFunc(name, unicode.IsControl) < 0
}
func New(id string, owner int64, r Request) (*Project, error) {
	if !bounded(strings.TrimSpace(r.Name), 200) {
		return nil, fail(400, "project name is required (at most 200 bytes)")
	}
	if r.Concurrency == 0 {
		r.Concurrency = 3
	}
	if r.Concurrency < 1 || r.Concurrency > 3 {
		return nil, fail(400, "concurrency must be 1 through 3")
	}
	return &Project{ID: id, OwnerID: owner, PostID: r.PostID, Name: r.Name, Status: "active", Concurrency: r.Concurrency, Members: []Member{{owner, "owner", "active"}}, Assignments: []Assignment{}, Artifacts: []Artifact{}, Integrations: []Integration{}, OutstandingChildren: []int64{}}, nil
}
func (p *Project) Apply(a Actor, r Request, now time.Time, newID func() string) (any, error) {
	p.Expire(now)
	p.RefreshDependencies()
	defer p.RefreshDependencies()
	if r.Action == "join" {
		if !a.Human() {
			return nil, fail(403, "an operator must accept membership")
		}
		for i := range p.Members {
			if p.Members[i].UserID == a.UserID && p.Members[i].Status == "invited" {
				p.Members[i].Status = "active"
				return p.Members[i], nil
			}
		}
		return nil, fail(409, "no pending invitation")
	}
	if err := p.CheckAccess(a); err != nil {
		return nil, err
	}
	if r.Action == "invite" || r.Action == "remove" || r.Action == "control" {
		if !a.Human() || a.UserID != p.OwnerID {
			return nil, fail(403, "project owner approval is required")
		}
		switch r.Action {
		case "invite":
			if r.UserID <= 0 || r.UserID == p.OwnerID || len(p.Members) >= 100 {
				return nil, fail(400, "choose another operator; maximum team size is 100")
			}
			if r.Role == "" {
				r.Role = "contributor"
			}
			if r.Role != "contributor" && r.Role != "coordinator" {
				return nil, fail(400, "role must be contributor or coordinator")
			}
			for _, m := range p.Members {
				if m.UserID == r.UserID && m.Status != "revoked" {
					return nil, fail(409, "operator already invited or active")
				}
				if r.Role == "coordinator" && m.Role == "coordinator" && m.Status != "revoked" {
					return nil, fail(409, "only one coordinator is allowed")
				}
			}
			member := Member{r.UserID, r.Role, "invited"}
			for i := range p.Members {
				if p.Members[i].UserID == r.UserID {
					p.Members[i] = member
					return member, nil
				}
			}
			p.Members = append(p.Members, member)
			return member, nil
		case "remove":
			if r.UserID == p.OwnerID {
				return nil, fail(400, "cannot remove the owner")
			}
			for i := range p.Members {
				if p.Members[i].UserID == r.UserID {
					p.Members[i].Status = "revoked"
					for j := range p.Assignments {
						if p.Assignments[j].Assignee == r.UserID {
							p.Invalidate(p.Assignments[j].ID)
							p.Assignments[j].State = "blocked"
							p.Assignments[j].Blocker = "operator removed"
							p.Assignments[j].NextAction = "assign an active operator"
						}
					}
					return p.Members[i], nil
				}
			}
			return nil, fail(404, "member not found")
		case "control":
			if r.Mode != "active" && r.Mode != "paused" && r.Mode != "cancelled" {
				return nil, fail(400, "mode must be active, paused, or cancelled")
			}
			if p.Status == "cancelled" {
				return nil, fail(409, "cancelled projects cannot resume")
			}
			p.Status = r.Mode
			if r.Mode != "active" {
				for i := range p.Assignments {
					s := &p.Assignments[i]
					if s.State == "running" {
						s.State = "blocked"
						s.Blocker = "project " + r.Mode
						s.NextAction = "owner must resume and retry"
						s.CurrentAttempt().State = "cancelled"
					}
				}
			}
			return map[string]any{"status": p.Status, "outstanding_children": p.OutstandingChildren}, nil
		}
	}
	if p.Status != "active" {
		return nil, fail(409, "project is "+p.Status)
	}
	switch r.Action {
	case "plan":
		if !p.Manages(a) {
			return nil, fail(403, "work management permission required")
		}
		if len(r.Assignments) == 0 || len(p.Assignments)+len(r.Assignments) > 100 {
			return nil, fail(400, "plan must contain 1 through 100 assignments")
		}
		for _, spec := range r.Assignments {
			if !ValidID(spec.ID) || p.Assignment(spec.ID) != nil || !bounded(spec.Title, 200) || !bounded(spec.Instructions, 32000) || p.Role(spec.Assignee) == "" {
				return nil, fail(400, "invalid assignment or inactive assignee")
			}
			if !a.Human() && !slices.Contains(a.AllowedMembers, spec.Assignee) {
				return nil, fail(403, "assignee is outside coordinator limits")
			}
			if spec.Kind == "" {
				spec.Kind = "work"
			}
			if spec.Kind != "work" && spec.Kind != "verification" {
				return nil, fail(400, "kind must be work or verification")
			}
			if spec.Kind == "verification" && len(spec.Dependencies) == 0 {
				return nil, fail(400, "verification requires an exact contribution dependency")
			}
			if len(spec.ExpectedOutputs) > 20 || !unique(spec.ExpectedOutputs) {
				return nil, fail(400, "expected outputs must be unique filenames (at most 20)")
			}
			for _, name := range spec.ExpectedOutputs {
				if !ValidFilename(name) {
					return nil, fail(400, "unsafe output filename")
				}
			}
			if !unique(spec.Inputs) {
				return nil, fail(400, "duplicate input")
			}
			for _, id := range spec.Inputs {
				if !p.CanReadArtifact(a, id) {
					return nil, fail(403, "input artifact is not accessible")
				}
			}
			p.Assignments = append(p.Assignments, Assignment{ID: spec.ID, Title: spec.Title, Instructions: spec.Instructions, Kind: spec.Kind, Assignee: spec.Assignee, Dependencies: append([]string{}, spec.Dependencies...), Inputs: append([]string{}, spec.Inputs...), ExpectedOutputs: append([]string{}, spec.ExpectedOutputs...), State: "queued", Attempts: []Attempt{}, Contributions: []Contribution{}, Thread: []Message{}})
		}
		if err := p.ValidateGraph(); err != nil {
			return nil, err
		}
		return p.Assignments, nil
	case "integrate":
		if a.UserID != p.OwnerID || !a.Can("manage") {
			return nil, fail(403, "lead owns integration")
		}
		if r.BaseRevision != p.Revision || len(r.Contributions) == 0 || !unique(r.Contributions) || !unique(r.Outputs) || len(r.Outputs) > 20 {
			return nil, fail(409, "refresh revision and select exact accepted contributions and outputs")
		}
		selected := map[string]*Contribution{}
		allowed := map[string]bool{}
		for i := range p.Assignments {
			s := &p.Assignments[i]
			if c := s.AcceptedContribution(); c != nil && slices.Contains(r.Contributions, c.ID) {
				if !p.Fresh(c) {
					return nil, fail(409, "stale contribution; retry and verify again")
				}
				selected[c.ID] = c
				for _, id := range c.Outputs {
					allowed[id] = true
				}
			}
		}
		if len(selected) != len(r.Contributions) {
			return nil, fail(409, "all selected contributions must be accepted")
		}
		for _, c := range selected {
			for _, dep := range c.Dependencies {
				if selected[dep] == nil {
					return nil, fail(409, "include each exact dependency in integration")
				}
			}
		}
		for _, id := range r.Outputs {
			if !allowed[id] {
				return nil, fail(403, "integration output must belong to selected accepted contributions")
			}
		}
		for _, s := range p.Assignments {
			if s.Kind == "verification" {
				for _, dep := range s.Dependencies {
					d := p.Assignment(dep)
					if d != nil && selected[d.Accepted] != nil && (s.AcceptedContribution() == nil || selected[s.Accepted] == nil) {
						return nil, fail(409, "accept and include verification of the selected patch")
					}
				}
			}
		}
		integration := Integration{newID(), p.Revision, p.Revision + 1, slices.Clone(r.Contributions), append([]string{}, r.Outputs...), r.Note, now}
		p.Revision++
		p.Integrations = append(p.Integrations, integration)
		return integration, nil
	}
	s := p.Assignment(r.AssignmentID)
	if s == nil {
		return nil, fail(404, "assignment not found")
	}
	if r.Action == "assign" || r.Action == "retry" || r.Action == "cancel" || r.Action == "accept" {
		if !p.Manages(a) {
			return nil, fail(403, "work management permission required")
		}
		if !a.Human() && !slices.Contains(a.AllowedMembers, s.Assignee) {
			return nil, fail(403, "assignment is outside coordinator limits")
		}
		switch r.Action {
		case "assign":
			if s.ChildPost != 0 {
				return nil, fail(409, "paid assignments retain their directed child worker")
			}
			if p.Role(r.UserID) == "" || !a.Human() && !slices.Contains(a.AllowedMembers, r.UserID) {
				return nil, fail(403, "assignee is outside approved team")
			}
			p.Invalidate(s.ID)
			s.Assignee = r.UserID
			return s, nil
		case "retry":
			if s.ChildPost != 0 {
				return nil, fail(409, "request a revision through the child task review")
			}
			p.Invalidate(s.ID)
			return s, nil
		case "cancel":
			p.Invalidate(s.ID)
			s.State = "cancelled"
			return s, nil
		case "accept":
			if s.State != "submitted" {
				return nil, fail(409, "assignment needs a submitted contribution")
			}
			for i := range s.Contributions {
				c := &s.Contributions[i]
				if c.ID == r.ContributionID && c.Status == "submitted" {
					if !p.Fresh(c) {
						return nil, fail(409, "verification or base revision is stale; retry")
					}
					c.Status = "accepted"
					s.Accepted = c.ID
					s.State = "accepted"
					return c, nil
				}
			}
			return nil, fail(404, "submitted contribution not found")
		}
	}
	if s.Assignee != a.UserID && !(r.Action == "message" && p.Manages(a)) {
		return nil, fail(403, "assignment is private to its assignee and lead")
	}
	switch r.Action {
	case "claim":
		if !a.Can("execute") {
			return nil, fail(403, "execute permission required")
		}
		if s.ChildPost != 0 {
			return nil, fail(409, "execute the funded child task using existing agent tools")
		}
		if s.State != "queued" {
			return nil, fail(409, "assignment is not queued")
		}
		if r.LeaseSeconds == 0 {
			r.LeaseSeconds = 90
		}
		if r.LeaseSeconds < 30 || r.LeaseSeconds > 300 {
			return nil, fail(400, "lease must be 30 through 300 seconds")
		}
		running := 0
		for _, s := range p.Assignments {
			if s.State == "running" {
				running++
			}
		}
		if running >= p.Concurrency {
			return nil, fail(409, "project concurrency limit reached")
		}
		inputs, deps, err := p.Snapshot(s)
		if err != nil {
			return nil, err
		}
		if s.Kind == "verification" {
			for id := range deps {
				if p.Assignment(id).Assignee == a.UserID {
					return nil, fail(403, "verification needs an independent operator")
				}
			}
		}
		at := Attempt{newID(), a.UserID, a.GrantID, now.Add(time.Duration(r.LeaseSeconds) * time.Second), "running", p.Revision, inputs, deps, now}
		s.Attempts = append(s.Attempts, at)
		s.State = "running"
		s.Blocker = ""
		s.NextAction = ""
		return at, nil
	case "renew", "block", "fail", "submit":
		at, err := p.LiveAttempt(a, s, r.AttemptID, now)
		if err != nil {
			return nil, err
		}
		switch r.Action {
		case "renew":
			at.LeaseUntil = now.Add(90 * time.Second)
			return at, nil
		case "block", "fail":
			if !bounded(r.Reason, 1000) || !bounded(r.NextAction, 1000) {
				return nil, fail(400, "reason and required next_action are mandatory")
			}
			s.State = "blocked"
			if r.Action == "fail" {
				s.State = "failed"
			}
			at.State = s.State
			s.Blocker = r.Reason
			s.NextAction = r.NextAction
			return s, nil
		case "submit":
			if r.BaseRevision != at.BaseRevision || !slices.Equal(r.Inputs, at.Inputs) || !unique(r.Outputs) || len(r.Outputs) > 20 {
				return nil, fail(409, "submit the attempt's exact base revision and input artifact IDs")
			}
			if len(r.Note) > 16000 || len(r.Evidence) > 30 {
				return nil, fail(400, "contribution note or evidence is too large")
			}
			names := map[string]bool{}
			for _, id := range r.Outputs {
				art := p.Artifact(id)
				if art == nil || art.AssignmentID != s.ID || art.AttemptID != at.ID || art.OwnerID != a.UserID || names[art.Name] {
					return nil, fail(403, "outputs must be unique artifacts uploaded by this attempt")
				}
				names[art.Name] = true
			}
			for _, name := range s.ExpectedOutputs {
				if !names[name] {
					return nil, fail(409, "missing expected output "+name)
				}
			}
			for _, e := range r.Evidence {
				if !bounded(e.Command, 4000) || !bounded(e.Result, 8000) || !bounded(e.Environment, 4000) {
					return nil, fail(400, "evidence requires command, result and environment")
				}
				if s.Kind == "verification" && e.ExitCode != 0 {
					return nil, fail(409, "failed checks cannot verify a contribution")
				}
			}
			if s.Kind == "verification" && len(r.Evidence) == 0 {
				return nil, fail(400, "verification requires command results and environment")
			}
			for key, value := range r.Usage {
				if !bounded(key, 80) || value < 0 {
					return nil, fail(400, "usage requires nonnegative reported counters")
				}
			}
			c := Contribution{ID: newID(), AssignmentID: s.ID, AttemptID: at.ID, OperatorID: a.UserID, BaseRevision: at.BaseRevision, Inputs: slices.Clone(at.Inputs), Outputs: append([]string{}, r.Outputs...), Dependencies: maps.Clone(at.Dependencies), Evidence: append([]Evidence{}, r.Evidence...), Note: r.Note, Status: "submitted", CreatedAt: now, Usage: r.Usage}
			if !p.Fresh(&c) {
				return nil, fail(409, "dependencies or base revision changed; retry")
			}
			s.Contributions = append(s.Contributions, c)
			s.State = "submitted"
			at.State = "submitted"
			return c, nil
		}
	case "message":
		if !a.Can("message") || !bounded(r.Note, 4000) || len(s.Thread) >= 1000 {
			return nil, fail(400, "message requires permission and 1 through 4000 bytes; thread limit 1000")
		}
		msg := Message{a.UserID, r.Note, now}
		s.Thread = append(s.Thread, msg)
		return msg, nil
	}
	return nil, fail(400, "unknown work operation")
}
