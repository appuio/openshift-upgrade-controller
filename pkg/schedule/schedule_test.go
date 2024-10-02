package schedule

import (
	"fmt"
	"testing"
	"time"

	"github.com/robfig/cron/v3"
	"github.com/stretchr/testify/require"
)

func Test_Schedule_Next(t *testing.T) {
	s, err := cron.ParseStandard("0 10 * * 6")
	require.NoError(t, err)

	subject := Schedule{
		Schedule: s,
		IsoWeek:  "@even",
	}

	expected, err := subject.Next(time.Date(2021, 1, 1, 0, 0, 0, 0, time.UTC))
	require.NoError(t, err)
	require.Equal(t, expected, time.Date(2021, 1, 16, 10, 0, 0, 0, time.UTC))
}

func Test_Schedule_Err(t *testing.T) {
	s, err := cron.ParseStandard("0 10 * * 6")
	require.NoError(t, err)

	subject := Schedule{
		Schedule: s,
		IsoWeek:  "@asdasd",
	}

	_, err = subject.Next(time.Date(2021, 1, 1, 0, 0, 0, 0, time.UTC))
	require.Error(t, err, "unknown iso week: asdasd")
}

func Test_Schedule_NextN(t *testing.T) {
	s, err := cron.ParseStandard("0 10 * * 6")
	require.NoError(t, err)

	subject := Schedule{
		Schedule: s,
		IsoWeek:  "@even",
	}

	tt := time.Date(2021, 1, 1, 0, 0, 0, 0, time.UTC)
	next, err := subject.NextN(tt, 10)
	require.NoError(t, err)
	require.Len(t, next, 10)

	expected := make([]string, 10)
	start := time.Date(2021, 1, 16, 10, 0, 0, 0, time.UTC)
	for i := range expected {
		expected[i] = start.Add(time.Hour * 24 * 7 * 2 * time.Duration(i)).Format(time.RFC3339)
	}
	got := make([]string, 10)
	for i := range next {
		got[i] = next[i].Format(time.RFC3339)
	}

	require.Equal(t, expected, got)
}

func Test_Schedule_NextN_Short(t *testing.T) {
	s, err := cron.ParseStandard("0 10 3 1 *")
	require.NoError(t, err)

	subject := Schedule{
		Schedule: s,
		IsoWeek:  "4",
	}

	tt := time.Date(2021, 1, 1, 0, 0, 0, 0, time.UTC)
	next, err := subject.NextN(tt, 10)
	require.Error(t, err, "no next time found")
	require.Len(t, next, 0)
}

func Test_checkIsoWeek(t *testing.T) {
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
			got, err := checkIsoWeek(c.t, c.schedISOWeek)
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
