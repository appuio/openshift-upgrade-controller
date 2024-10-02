package schedule

import (
	"fmt"
	"strconv"
	"time"

	"github.com/robfig/cron/v3"
)

// ErrNoNextFound is returned when no no next time could be found
var ErrNoNextFound = fmt.Errorf("no next time found")

// Schedule is a wrapper around cron.Schedule that adds the IsoWeek field
type Schedule struct {
	cron.Schedule

	// The week of the year to run the job
	// 1-53 or @odd, @even
	// Empty matches any week of the year
	IsoWeek string
}

// NextN returns the next n activation times of the schedule after the earliest time.
// If the schedule is invalid, it will return an error.
// If the schedule is valid but no next time could be found (too far in the future), it will return an error with ErrNoNextFound.
// If an error occurs the returned slice will contain len() valid activation times that were found before the error occurred.
func (s Schedule) NextN(earliest time.Time, n int) ([]time.Time, error) {
	nextTimes := make([]time.Time, 0, n)

	for i := 0; i < n; i++ {
		nextTime, err := s.Next(earliest)
		if err != nil {
			return nextTimes, err
		}
		nextTimes = append(nextTimes, nextTime)
		earliest = nextTime
	}
	return nextTimes, nil
}

// Next returns the next activation time of the schedule after the earliest time.
// If the schedule is invalid, it will return an error.
// If the schedule is valid but no next time could be found (too far in the future), it will return an error with ErrNoNextFound.
func (s Schedule) Next(earliest time.Time) (time.Time, error) {
	n := s.Schedule.Next(earliest)
	// if the next activation time is more than 1000 runs away, we assume that the cron schedule is invalid as a safe guard
	for i := 0; i < 1000; i++ {
		isoWeekOK, err := checkIsoWeek(n, s.IsoWeek)
		if err != nil {
			return time.Time{}, err
		}
		if isoWeekOK {
			return n, nil
		}
		n = s.Schedule.Next(n)
	}
	return time.Time{}, fmt.Errorf("could not find next scheduled time, checked until %q: %w", n, ErrNoNextFound)
}

// checkIsoWeek checks if the given time is in the given iso week.
// The iso week can be one of the following:
// - "": every iso week
// - "@even": every even iso week
// - "@odd": every odd iso week
// - "<N>": every iso week N
func checkIsoWeek(t time.Time, schedISOWeek string) (bool, error) {
	_, iw := t.ISOWeek()
	switch schedISOWeek {
	case "":
		return true, nil
	case "@even":
		return iw%2 == 0, nil
	case "@odd":
		return iw%2 == 1, nil
	}

	nw, err := strconv.ParseInt(schedISOWeek, 10, 64)
	if err == nil {
		return nw == int64(iw), nil
	}
	return false, fmt.Errorf("unknown iso week: %s", schedISOWeek)
}
