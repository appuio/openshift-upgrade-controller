package state

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestAmbigousMatch(t *testing.T) {
	noop := func(ctx context.Context, t any) (string, any, error) { return "", nil, nil }

	for _, tc := range []struct {
		name    string
		states  []State[any, any]
		wantErr error
	}{
		{
			name:    "no states",
			states:  []State[any, any]{},
			wantErr: nil,
		},
		{
			name: "one state",
			states: []State[any, any]{
				{Name: "state1", F: noop, Match: []MatchCondition{{Type: "Ready", Status: "True"}}},
			},
			wantErr: nil,
		},
		{
			name: "one empty state",
			states: []State[any, any]{
				{Name: "state1", F: noop, Match: []MatchCondition{}},
				{Name: "state2", F: noop, Match: []MatchCondition{{Type: "Ready", Status: "True"}}},
			},
			wantErr: ErrAmbigousMatch,
		},
		{
			name: "two states with different match conditions",
			states: []State[any, any]{
				{Name: "state1", F: noop, Match: []MatchCondition{{Type: "Ready", Status: "True"}}},
				{Name: "state2", F: noop, Match: []MatchCondition{{Type: "Healthy", Status: "True"}}},
			},
			wantErr: ErrAmbigousMatch,
		},
		{
			name: "two states with different match conditions, Type is same but Status is different",
			states: []State[any, any]{
				{Name: "state1", F: noop, Match: []MatchCondition{{Type: "Ready", Status: "True"}}},
				{Name: "state2", F: noop, Match: []MatchCondition{{Type: "Ready", Status: "False"}}},
			},
			wantErr: nil,
		},
		{
			name: "two states with partially different match conditions, Type is same but Status is different",
			states: []State[any, any]{
				{Name: "state1", F: noop, Match: []MatchCondition{{Type: "Healthy", Status: "True"}, {Type: "Ready", Status: "True"}}},
				{Name: "state2", F: noop, Match: []MatchCondition{{Type: "Healthy", Status: "True"}, {Type: "Ready", Status: "False"}}},
			},
			wantErr: nil,
		},
		{
			name: "two states with partially different match conditions, Type is same but Status is different",
			states: []State[any, any]{
				{Name: "state1", F: noop, Match: []MatchCondition{{Type: "Ready", Status: "True"}, {Type: "Healthy", Status: "True"}}},
				{Name: "state2", F: noop, Match: []MatchCondition{{Type: "Ready", Status: "False"}, {Type: "Healthy", Status: "True"}}},
			},
			wantErr: nil,
		},
		{
			name: "two states with partially different match conditions, Type is same but Status is different",
			states: []State[any, any]{
				{Name: "state1", F: noop, Match: []MatchCondition{{Type: "Ready", Status: "True"}, {Type: "Healthy", Status: "True"}}},
				{Name: "state2", F: noop, Match: []MatchCondition{{Type: "Healthy", Status: "True"}, {Type: "Ready", Status: "False"}}},
			},
			wantErr: nil,
		},
		{
			name: "two states with same match conditions",
			states: []State[any, any]{
				{Name: "state1", F: noop, Match: []MatchCondition{{Type: "Ready", Status: "True"}}},
				{Name: "state2", F: noop, Match: []MatchCondition{{Type: "Ready", Status: "True"}}},
			},
			wantErr: ErrAmbigousMatch,
		},
		{
			name: "two states with same match conditions in different order",
			states: []State[any, any]{
				{Name: "state1", F: noop, Match: []MatchCondition{{Type: "Ready", Status: "True"}, {Type: "Healthy", Status: "True"}}},
				{Name: "state2", F: noop, Match: []MatchCondition{{Type: "Healthy", Status: "True"}, {Type: "Ready", Status: "True"}}},
			},
			wantErr: ErrAmbigousMatch,
		},
		{
			name: "two states with different match conditions but one is subset of the other",
			states: []State[any, any]{
				{Name: "state1", F: noop, Match: []MatchCondition{{Type: "Ready", Status: "True"}}},
				{Name: "state2", F: noop, Match: []MatchCondition{{Type: "Ready", Status: "True"}, {Type: "Healthy", Status: "True"}}},
			},
			wantErr: ErrAmbigousMatch,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			builder := &Builder[any, any]{}
			for _, state := range tc.states {
				builder.AddState(state.Name, state.F, state.Match...)
			}

			_, err := builder.Build()
			if tc.wantErr == nil && err != nil {
				t.Fatalf("expected no error, got %v", err)
			}
			if tc.wantErr != nil && err == nil {
				t.Fatalf("expected error %v, got nil", tc.wantErr)
			}
			if tc.wantErr != nil && err != nil && !errors.Is(err, tc.wantErr) {
				t.Fatalf("expected error %v, got %v", tc.wantErr, err)
			}
		})
	}
}

func TestGraphviz(t *testing.T) {
	builder := &Builder[any, any]{}
	builder.AddState("state1", nil, MatchCondition{Type: "Ready", Status: "True"})
	builder.AddState("state2", nil, MatchCondition{Type: "Ready", Status: "False"})
	builder.AddState("state3", nil, MatchCondition{Type: "Healthy", Status: "True"}, MatchCondition{Type: "Running", Status: "True"})

	builder.AddTransition("state1", "state2", nil)
	builder.AddTransition("state2", "state3", nil)
	builder.AddTransition("state3", "state1", nil)

	graphviz, err := builder.Graphviz()
	require.NoError(t, err)
	expected := `digraph G {
  state1 [label="state1\n\nReady=True"];
  state2 [label="state2\n\nReady=False"];
  state3 [label="state3\n\nHealthy=True\nRunning=True"];
  state1 -> state2;
  state2 -> state3;
  state3 -> state1;
}
`
	require.Equal(t, expected, graphviz)
}
