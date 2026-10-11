package work

import (
	"fmt"
	"slices"
	"strings"
	"time"
)

type Failure struct {
	Status  int
	Message string
}

func (e *Failure) Error() string            { return e.Message }
func fail(status int, message string) error { return &Failure{status, message} }

type Member struct {
	UserID int64  `json:"user_id"`
	Role   string `json:"role"`
	Status string `json:"status"`
}
type Artifact struct {
	ID           string `json:"id"`
	Name         string `json:"name"`
	SHA256       string `json:"sha256"`
	Size         int64  `json:"size"`
	OwnerID      int64  `json:"owner_id"`
	AssignmentID string `json:"assignment_id,omitempty"`
	AttemptID    string `json:"attempt_id,omitempty"`
	SourcePost   int64  `json:"source_post,omitempty"`
	SourceFile   string `json:"source_file,omitempty"`
}
type Evidence struct {
	Command     string `json:"command"`
	ExitCode    int    `json:"exit_code"`
	Result      string `json:"result"`
	Environment string `json:"environment"`
}
type Attempt struct {
	ID           string            `json:"id"`
	OperatorID   int64             `json:"operator_id"`
	GrantID      string            `json:"grant_id,omitempty"`
	LeaseUntil   time.Time         `json:"lease_until"`
	State        string            `json:"state"`
	BaseRevision int64             `json:"base_revision"`
	Inputs       []string          `json:"inputs"`
	Dependencies map[string]string `json:"dependencies"`
	StartedAt    time.Time         `json:"started_at"`
}
type Contribution struct {
	ID           string            `json:"id"`
	AssignmentID string            `json:"assignment_id"`
	AttemptID    string            `json:"attempt_id"`
	OperatorID   int64             `json:"operator_id"`
	BaseRevision int64             `json:"base_revision"`
	Inputs       []string          `json:"inputs"`
	Outputs      []string          `json:"outputs"`
	Dependencies map[string]string `json:"dependencies"`
	Evidence     []Evidence        `json:"evidence"`
	Note         string            `json:"note"`
	Status       string            `json:"status"`
	CreatedAt    time.Time         `json:"created_at"`
	Usage        map[string]int64  `json:"usage,omitempty"`
	ChildPost    int64             `json:"child_post,omitempty"`
	ChildVersion int64             `json:"child_version,omitempty"`
}
type Message struct {
	ActorID   int64     `json:"actor_id"`
	Text      string    `json:"text"`
	CreatedAt time.Time `json:"created_at"`
}
type Assignment struct {
	ID              string         `json:"id"`
	Title           string         `json:"title"`
	Instructions    string         `json:"instructions"`
	Kind            string         `json:"kind"`
	Assignee        int64          `json:"assignee"`
	Dependencies    []string       `json:"dependencies"`
	Inputs          []string       `json:"inputs"`
	ExpectedOutputs []string       `json:"expected_outputs"`
	State           string         `json:"state"`
	Blocker         string         `json:"blocker"`
	NextAction      string         `json:"next_action"`
	Attempts        []Attempt      `json:"attempts"`
	Contributions   []Contribution `json:"contributions"`
	Accepted        string         `json:"accepted_contribution,omitempty"`
	Thread          []Message      `json:"thread"`
	ChildPost       int64          `json:"child_post,omitempty"`
}
type Integration struct {
	ID            string    `json:"id"`
	BaseRevision  int64     `json:"base_revision"`
	Revision      int64     `json:"revision"`
	Contributions []string  `json:"contributions"`
	Outputs       []string  `json:"outputs"`
	Note          string    `json:"note"`
	CreatedAt     time.Time `json:"created_at"`
}
type Project struct {
	ID                  string        `json:"id"`
	OwnerID             int64         `json:"owner_id"`
	PostID              int64         `json:"post_id,omitempty"`
	Name                string        `json:"name"`
	Status              string        `json:"status"`
	Concurrency         int           `json:"concurrency"`
	Revision            int64         `json:"revision"`
	Cursor              int64         `json:"cursor"`
	Members             []Member      `json:"members"`
	Assignments         []Assignment  `json:"assignments"`
	Artifacts           []Artifact    `json:"artifacts"`
	Integrations        []Integration `json:"integrations"`
	OutstandingChildren []int64       `json:"outstanding_children"`
}

type Actor struct {
	UserID         int64
	GrantID        string
	Permissions    []string
	AllowedMembers []int64
}

func (a Actor) Human() bool { return a.GrantID == "" }
func (a Actor) Can(permission string) bool {
	return a.Human() || slices.Contains(a.Permissions, permission)
}
func (p *Project) Role(user int64) string {
	for _, m := range p.Members {
		if m.UserID == user && m.Status == "active" {
			return m.Role
		}
	}
	return ""
}
func (p *Project) Manages(a Actor) bool {
	return (p.Role(a.UserID) == "owner" || p.Role(a.UserID) == "coordinator") && a.Can("manage")
}
func (p *Project) Assignment(id string) *Assignment {
	for i := range p.Assignments {
		if p.Assignments[i].ID == id {
			return &p.Assignments[i]
		}
	}
	return nil
}
func (p *Project) Artifact(id string) *Artifact {
	for i := range p.Artifacts {
		if p.Artifacts[i].ID == id {
			return &p.Artifacts[i]
		}
	}
	return nil
}
func (s *Assignment) CurrentAttempt() *Attempt {
	if len(s.Attempts) == 0 {
		return nil
	}
	return &s.Attempts[len(s.Attempts)-1]
}
func (s *Assignment) AcceptedContribution() *Contribution {
	for i := range s.Contributions {
		if s.Contributions[i].ID == s.Accepted && s.Contributions[i].Status == "accepted" {
			return &s.Contributions[i]
		}
	}
	return nil
}
func (p *Project) CheckAccess(a Actor) error {
	if p.Role(a.UserID) == "" {
		return fail(403, "active project membership required")
	}
	return nil
}
func (p *Project) CanReadArtifact(a Actor, id string) bool {
	if p.Role(a.UserID) == "" || p.Artifact(id) == nil {
		return false
	}
	if p.Manages(a) || p.OwnerID == a.UserID {
		return true
	}
	for i := range p.Assignments {
		s := &p.Assignments[i]
		if s.Assignee != a.UserID {
			continue
		}
		if slices.Contains(s.Inputs, id) {
			return true
		}
		for _, attempt := range s.Attempts {
			if slices.Contains(attempt.Inputs, id) {
				return true
			}
		}
		for _, c := range s.Contributions {
			if slices.Contains(c.Outputs, id) {
				return true
			}
		}
		for _, dep := range s.Dependencies {
			if d := p.Assignment(dep); d != nil {
				if c := d.AcceptedContribution(); c != nil && slices.Contains(c.Outputs, id) {
					return true
				}
			}
		}
	}
	art := p.Artifact(id)
	return art.OwnerID == a.UserID && art.AssignmentID != "" && p.Assignment(art.AssignmentID) != nil && p.Assignment(art.AssignmentID).Assignee == a.UserID
}

// View copies before filtering so restricted reads never mutate stored state.
func (p Project) View(a Actor) Project {
	if p.Manages(a) || p.OwnerID == a.UserID {
		return p
	}
	p.Members = slices.DeleteFunc(slices.Clone(p.Members), func(m Member) bool { return m.UserID != a.UserID })
	all := p
	p.Assignments = slices.DeleteFunc(slices.Clone(p.Assignments), func(s Assignment) bool { return s.Assignee != a.UserID })
	p.Artifacts = slices.DeleteFunc(slices.Clone(p.Artifacts), func(art Artifact) bool { return !all.CanReadArtifact(a, art.ID) })
	p.Integrations = []Integration{}
	p.OutstandingChildren = []int64{}
	return p
}
func (p *Project) Snapshot(s *Assignment) ([]string, map[string]string, error) {
	inputs := slices.Clone(s.Inputs)
	deps := map[string]string{}
	for _, id := range s.Dependencies {
		d := p.Assignment(id)
		if d == nil || d.State != "accepted" || d.AcceptedContribution() == nil {
			return nil, nil, fail(409, "dependency "+id+" needs an accepted contribution")
		}
		c := d.AcceptedContribution()
		deps[id] = c.ID
		for _, art := range c.Outputs {
			if !slices.Contains(inputs, art) {
				inputs = append(inputs, art)
			}
		}
	}
	if inputs == nil {
		inputs = []string{}
	}
	return inputs, deps, nil
}
func (p *Project) Fresh(c *Contribution) bool {
	if c.BaseRevision != p.Revision {
		return false
	}
	for id, version := range c.Dependencies {
		s := p.Assignment(id)
		if s == nil || s.State != "accepted" || s.Accepted != version {
			return false
		}
	}
	return true
}
func (p *Project) Invalidate(id string) {
	s := p.Assignment(id)
	if s == nil {
		return
	}
	s.Accepted = ""
	s.State = "queued"
	s.Blocker = ""
	s.NextAction = ""
	if attempt := s.CurrentAttempt(); attempt != nil {
		attempt.State = "replaced"
	}
	for i := range s.Contributions {
		s.Contributions[i].Status = "stale"
	}
	for i := range p.Assignments {
		if slices.Contains(p.Assignments[i].Dependencies, id) {
			p.Invalidate(p.Assignments[i].ID)
		}
	}
}
func (p *Project) ValidateGraph() error {
	visiting, done := map[string]bool{}, map[string]bool{}
	var walk func(string) error
	walk = func(id string) error {
		if visiting[id] {
			return fail(400, "assignment dependencies contain a cycle")
		}
		if done[id] {
			return nil
		}
		s := p.Assignment(id)
		if s == nil {
			return fail(400, "unknown dependency "+id)
		}
		visiting[id] = true
		seen := map[string]bool{}
		for _, dep := range s.Dependencies {
			if seen[dep] {
				return fail(400, "duplicate dependency")
			}
			seen[dep] = true
			if err := walk(dep); err != nil {
				return err
			}
		}
		visiting[id] = false
		done[id] = true
		return nil
	}
	for _, s := range p.Assignments {
		if err := walk(s.ID); err != nil {
			return err
		}
	}
	return nil
}
func (p *Project) LiveAttempt(a Actor, s *Assignment, id string, now time.Time) (*Attempt, error) {
	if s.Assignee != a.UserID || !a.Can("execute") {
		return nil, fail(403, "only the assigned operator may execute")
	}
	attempt := s.CurrentAttempt()
	if s.State != "running" || attempt == nil || attempt.ID != id || attempt.OperatorID != a.UserID || attempt.GrantID != a.GrantID || attempt.State != "running" || !now.Before(attempt.LeaseUntil) {
		return nil, fail(409, "attempt expired or replaced; claim a fresh attempt")
	}
	return attempt, nil
}
func (p *Project) Expire(now time.Time) {
	for i := range p.Assignments {
		s := &p.Assignments[i]
		if at := s.CurrentAttempt(); s.State == "running" && at != nil && !now.Before(at.LeaseUntil) {
			at.State = "expired"
			s.State = "queued"
			s.Blocker = "lease expired"
			s.NextAction = "claim a fresh attempt"
		}
	}
}
func unique(items []string) bool {
	seen := map[string]bool{}
	for _, id := range items {
		if id == "" || seen[id] {
			return false
		}
		seen[id] = true
	}
	return true
}
func bounded(text string, max int) bool { return len(text) > 0 && len(text) <= max }
func (p Project) String() string        { return fmt.Sprintf("%s (%s)", p.Name, p.ID) }

// Dependency blockers advance only when exact upstream contributions are accepted.
func (p *Project) RefreshDependencies() {
	for i := range p.Assignments {
		s := &p.Assignments[i]
		if s.ChildPost != 0 || (s.State != "queued" && !(s.State == "blocked" && strings.HasPrefix(s.Blocker, "dependency "))) {
			continue
		}
		_, _, err := p.Snapshot(s)
		if err != nil {
			s.State = "blocked"
			s.Blocker = err.Error()
			s.NextAction = "lead accepts the required dependency contribution"
		} else if s.State == "blocked" {
			s.State = "queued"
			s.Blocker = ""
			s.NextAction = ""
		}
	}
}
