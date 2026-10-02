// Package skills holds the playbooks. Each is a template DAG the planner
// instantiates into durable tasks and may tailor — within the tools the skill
// allows. See docs/01-intents-tools-skills.md §3.
//
// The studio's build_game playbook (Designer → Engineer → Playtester → Critic
// → publish approval) registers here once the script runtime lands. Until
// then the registry holds one generic playbook, which is also the planner's
// fallback for a goal whose skill is unknown.
package skills

import (
	"sort"
	"sync"
)

type Step struct {
	Key          string
	Title        string
	Instructions string
	Tools        []string
	Deps         []string
	// Verify names deterministic checks the worker runs on the step's result
	// before accepting it (see engine.RegisterVerifier).
	Verify []string
	// WaitDays turns the step into a timer: it succeeds WaitDays after its
	// dependencies finish.
	WaitDays int
	MaxSteps int
}

type Skill struct {
	Name        string
	Domain      string // studio | general
	Title       string
	TitleZH     string
	Description string
	Criteria    []string
	Milestones  []string
	Steps       []Step
	// Recurring skills re-plan themselves after the last step instead of
	// finishing, until the owner closes them.
	Recurring bool
}

var (
	mu  sync.RWMutex
	all = map[string]*Skill{}
)

// Register adds a playbook. A duplicate name is a programming error.
func Register(s *Skill) {
	mu.Lock()
	defer mu.Unlock()
	if _, dup := all[s.Name]; dup {
		panic("skill registered twice: " + s.Name)
	}
	all[s.Name] = s
}

func Get(name string) (*Skill, bool) {
	mu.RLock()
	defer mu.RUnlock()
	s, ok := all[name]
	return s, ok
}

func All() []*Skill {
	mu.RLock()
	defer mu.RUnlock()
	out := make([]*Skill, 0, len(all))
	for _, s := range all {
		out = append(out, s)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

// Fallback is the playbook the planner uses for a goal whose skill is not
// registered (e.g. a goal created by a newer build than the worker running it).
const Fallback = "general-task"

// Base are the tools every playbook may use.
var Base = []string{"memory_save", "notify_user"}

func init() {
	Register(&Skill{Name: Fallback, Domain: "general", Title: "General task", TitleZH: "通用任务",
		Description: "Any multi-step request that does not fit a specific playbook.",
		Criteria:    []string{"The player's request is fulfilled and they were told the result"},
		Milestones:  []string{"Work", "Report"},
		Steps: []Step{
			{Key: "work", Title: "Do the work", Tools: Base, Instructions: "Complete the player's request with the tools you have. If it cannot be done, say exactly why.", MaxSteps: 12},
			{Key: "report", Title: "Report back", Deps: []string{"work"}, Tools: Base, Instructions: "Tell the player the result with notify_user, in their language."},
		}})
}
