package processing

import "time"

const contentMaxAttempts = 3

// Schedule defines the daily processing window in its configured location.
// Windows whose start is after their end cross midnight.
type Schedule struct {
	Immediate bool
	Location  *time.Location
	Start     time.Duration
	End       time.Duration
}

// Allows reports whether automatic processing may begin at value.
func (s Schedule) Allows(value time.Time) bool {
	if s.Immediate {
		return true
	}
	local := value.In(s.Location)
	current := time.Duration(local.Hour())*time.Hour + time.Duration(local.Minute())*time.Minute + time.Duration(local.Second())*time.Second
	if s.Start < s.End {
		return current >= s.Start && current < s.End
	}
	return current >= s.Start || current < s.End
}

func transientRetryDelay(attempt int) time.Duration {
	delays := []time.Duration{30 * time.Second, 2 * time.Minute, 10 * time.Minute, 30 * time.Minute, time.Hour, 2 * time.Hour}
	return delayAt(delays, attempt)
}

func configurationRetryDelay(attempt int) time.Duration {
	delays := []time.Duration{10 * time.Minute, 30 * time.Minute, time.Hour}
	return delayAt(delays, attempt)
}

func contentRetryDelay(attempt int) time.Duration {
	delays := []time.Duration{time.Minute, 10 * time.Minute, time.Hour}
	return delayAt(delays, attempt)
}

func delayAt(delays []time.Duration, attempt int) time.Duration {
	if attempt < 1 {
		attempt = 1
	}
	if attempt > len(delays) {
		attempt = len(delays)
	}
	return delays[attempt-1]
}

// jitter deterministically varies endpoint-wide retries by no more than 20%.
func jitter(delay time.Duration, jobID int64, attempt int) time.Duration {
	percentage := (jobID*31+int64(attempt)*17)%41 - 20
	return delay + time.Duration(int64(delay)*percentage/100)
}
