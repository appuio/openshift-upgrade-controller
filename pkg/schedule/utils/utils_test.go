package scheduleutils

import (
	"fmt"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestCheckIsoWeek(t *testing.T) {
	tc := []struct {
		t            time.Time
		schedISOWeek string
		expected     bool
		expectedErr  error
	}{
		{
			t:            time.Date(2021, 1, 1, 0, 0, 0, 0, time.UTC),
			schedISOWeek: "",
			expected:     true,
		},
		{
			t:            time.Date(2021, 1, 1, 0, 0, 0, 0, time.UTC),
			schedISOWeek: "@even",
			expected:     false,
		},
		{
			t:            time.Date(2021, 1, 13, 0, 0, 0, 0, time.UTC),
			schedISOWeek: "@even",
			expected:     true,
		},
		{
			t:            time.Date(2021, 1, 1, 0, 0, 0, 0, time.UTC),
			schedISOWeek: "@odd",
			expected:     true,
		},
		{
			t:            time.Date(2021, 1, 13, 0, 0, 0, 0, time.UTC),
			schedISOWeek: "@odd",
			expected:     false,
		},
		{
			t:            time.Date(2021, 1, 1, 0, 0, 0, 0, time.UTC),
			schedISOWeek: "53",
			expected:     true,
		},
		{
			t:            time.Date(2021, 1, 1, 0, 0, 0, 0, time.UTC),
			schedISOWeek: "1",
			expected:     false,
		},
		{
			t:            time.Date(2021, 1, 1, 0, 0, 0, 0, time.UTC),
			schedISOWeek: "invalid",
			expectedErr:  fmt.Errorf("unknown iso week: invalid"),
		},
	}

	for _, c := range tc {
		_, iw := c.t.ISOWeek()
		t.Run(fmt.Sprintf("sched: %s, iso week from time: %d", c.schedISOWeek, iw), func(t *testing.T) {
			got, err := CheckIsoWeek(c.t, c.schedISOWeek)
			if err != nil {
				if c.expectedErr == nil {
					t.Fatalf("unexpected error: %v", err)
				}
				require.Equal(t, c.expectedErr.Error(), err.Error())
				return
			}
			if c.expectedErr != nil {
				t.Fatalf("expected error %q, got nil", c.expectedErr)
			}
			require.Equal(t, c.expected, got)
		})
	}
}
