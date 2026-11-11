package clock

import "time"

// Clock reports the current time. Every garret package that needs the time
// receives a Clock instead of calling time.Now, so tests can control it.
type Clock interface {
	Now() time.Time
}

// System is the Clock backed by the operating system. It is safe for
// concurrent use.
type System struct{}

// Now returns time.Now(). It is the only call to time.Now in garret.
func (System) Now() time.Time {
	return time.Now()
}
