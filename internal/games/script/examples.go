package script

import (
	"embed"
	"fmt"
	"path"
	"sort"
	"strings"
	"sync"
)

//go:embed examples/*.js
var exampleFS embed.FS

// Example is a bundled module: a seed for the community catalog and a
// few-shot template for the LLM Engineer.
type Example struct {
	ID      string
	Name    string
	Summary string
	Source  string
}

var (
	examplesOnce sync.Once
	examples     []Example
	examplesErr  error
)

// Examples returns the bundled example modules, sorted by ID. Name and
// Summary come from each module's own meta, so they cannot drift.
func Examples() []Example {
	examplesOnce.Do(func() {
		entries, err := exampleFS.ReadDir("examples")
		if err != nil {
			examplesErr = err
			return
		}
		for _, e := range entries {
			if e.IsDir() || !strings.HasSuffix(e.Name(), ".js") {
				continue
			}
			src, err := exampleFS.ReadFile(path.Join("examples", e.Name()))
			if err != nil {
				examplesErr = err
				return
			}
			id := strings.TrimSuffix(e.Name(), ".js")
			g, err := Load(id, string(src), Options{PoolSize: 1})
			if err != nil {
				examplesErr = fmt.Errorf("example %s: %w", id, err)
				return
			}
			m := g.Meta()
			examples = append(examples, Example{ID: id, Name: m.Name, Summary: m.Summary, Source: string(src)})
		}
		sort.Slice(examples, func(i, j int) bool { return examples[i].ID < examples[j].ID })
	})
	if examplesErr != nil {
		// The examples are embedded and covered by tests; failing here means
		// a broken build, not a runtime condition to handle.
		panic(examplesErr)
	}
	return append([]Example(nil), examples...)
}

// ExampleByID returns one bundled example.
func ExampleByID(id string) (Example, bool) {
	for _, e := range Examples() {
		if e.ID == id {
			return e, true
		}
	}
	return Example{}, false
}
