package scheduleutils

import (
	"fmt"
	"strconv"
	"time"
)

// CheckIsoWeek checks if the given time is in the given iso week.
// The iso week can be one of the following:
// - "": every iso week
// - "@even": every even iso week
// - "@odd": every odd iso week
// - "<N>": every iso week N
func CheckIsoWeek(t time.Time, schedISOWeek string) (bool, error) {
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
