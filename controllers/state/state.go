package state

import (
	"context"
	"fmt"
	"strings"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

var ErrAmbigousMatch = fmt.Errorf("ambigous match conditions")

type MatchCondition struct {
	Type   string
	Status metav1.ConditionStatus
}

type State[T, R any] struct {
	Name  string
	F     func(context.Context, T) (string, R, error)
	Match []MatchCondition
}

type Builder[T, R any] struct {
	states      []State[T, R]
	transitions map[[2]string]func(*[]metav1.Condition) error
}

type Machine[T, R any] struct {
	states      []State[T, R]
	transitions map[[2]string]func(*[]metav1.Condition) error
}

func (b *Builder[T, R]) AddState(name string, f func(context.Context, T) (string, R, error), match ...MatchCondition) *Builder[T, R] {
	b.states = append(b.states, State[T, R]{
		Name:  name,
		F:     f,
		Match: match,
	})
	return b
}

func (b *Builder[T, R]) AddTransition(from, to string, f func(*[]metav1.Condition) error) *Builder[T, R] {
	if b.transitions == nil {
		b.transitions = make(map[[2]string]func(*[]metav1.Condition) error)
	}
	b.transitions[[2]string{from, to}] = f
	return b
}

func (b *Builder[T, R]) Build() (*Machine[T, R], error) {
	ambigousStates := make([][2]string, 0)
	for i := range b.states {
		for j := i + 1; j < len(b.states); j++ {
			if ambiguousMatch(b.states[i].Match, b.states[j].Match) {
				ambigousStates = append(ambigousStates, [2]string{b.states[i].Name, b.states[j].Name})
			}
		}
	}
	if len(ambigousStates) > 0 {
		return nil, fmt.Errorf("%w: %v", ErrAmbigousMatch, ambigousStates)
	}

	return &Machine[T, R]{
		states:      b.states,
		transitions: b.transitions,
	}, nil
}

// Graphviz returns a Graphviz representation of the state machine. This is useful for debugging and visualization purposes.
func (b *Builder[T, R]) Graphviz() (string, error) {
	s := strings.Builder{}
	s.WriteString("digraph G {\n")
	for _, state := range b.states {
		lbl := strings.Builder{}
		lbl.WriteString(state.Name)
		if len(state.Match) > 0 {
			lbl.WriteString("\\n")
		}
		for _, m := range state.Match {
			lbl.WriteString(fmt.Sprintf("\\n%s=%s", m.Type, m.Status))
		}
		s.WriteString(fmt.Sprintf("  %s [label=\"%s\"];\n", state.Name, lbl.String()))
	}
	for transition := range b.transitions {
		s.WriteString(fmt.Sprintf("  %s -> %s;\n", transition[0], transition[1]))
	}
	s.WriteString("}\n")
	return s.String(), nil
}

func (m *Machine[T, R]) Run(ctx context.Context, cond *[]metav1.Condition, input T) (R, error) {
	state, ok := findStateByMatch(m.states, cond)
	if !ok {
		var zero R
		return zero, fmt.Errorf("no matching state found")
	}
	newState, r, err := state.F(ctx, input)
	if newState != "" {
		trans, ok := m.transitions[[2]string{state.Name, newState}]
		if !ok {
			var zero R
			return zero, fmt.Errorf("invalid transition from '%s' to '%s'", state.Name, newState)
		}
		newStateObj, ok := findStateByName(m.states, newState)
		if !ok {
			var zero R
			return zero, fmt.Errorf("no matching state found for new state '%s'", newState)
		}
		if err := trans(cond); err != nil {
			var zero R
			return zero, fmt.Errorf("transition from '%s' to '%s' failed: %w", state.Name, newState, err)
		}
		if !matches(newStateObj.Match, cond) {
			var zero R
			return zero, fmt.Errorf("after transition from '%s' to '%s', conditions do not match the new state", state.Name, newState)
		}
	}
	return r, err
}

func findStateByName[T, R any](states []State[T, R], name string) (State[T, R], bool) {
	for _, state := range states {
		if state.Name == name {
			return state, true
		}
	}
	var zero State[T, R]
	return zero, false
}

func findStateByMatch[T, R any](states []State[T, R], cond *[]metav1.Condition) (State[T, R], bool) {
	for _, state := range states {
		if matches(state.Match, cond) {
			return state, true
		}
	}
	var zero State[T, R]
	return zero, false
}

func matches(match []MatchCondition, cond *[]metav1.Condition) bool {
	for _, m := range match {
		found := false
		for _, c := range *cond {
			if c.Type == m.Type && c.Status == m.Status {
				found = true
				break
			}
		}
		if !found {
			return false
		}
	}

	return true
}

func ambiguousMatch(a, b []MatchCondition) bool {
	am := make(map[string]metav1.ConditionStatus, len(a))
	bm := make(map[string]metav1.ConditionStatus, len(b))

	for _, cond := range a {
		am[cond.Type] = cond.Status
	}
	for _, cond := range b {
		bm[cond.Type] = cond.Status
	}

	// Check if there's any contradiction (same Type with different Status)
	for t, status := range am {
		if bStatus, ok := bm[t]; ok && bStatus != status {
			// Contradiction found - conditions can't both be true
			return false
		}
	}

	// No contradiction - conditions could both be true simultaneously
	return true
}
