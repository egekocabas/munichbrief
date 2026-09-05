package processing

import (
	"testing"
	"time"
)

func TestRetrySchedulesAndJitterBounds(t *testing.T) {
	if transientRetryDelay(1) != 30*time.Second || transientRetryDelay(99) != 5*time.Minute {
		t.Fatal("transient retry schedule is incorrect")
	}
	if configurationRetryDelay(1) != 5*time.Minute || contentRetryDelay(3) != 5*time.Minute {
		t.Fatal("configuration or content retry schedule is incorrect")
	}
	base := 2 * time.Minute
	value := jitter(base, 42, 3)
	if value < 96*time.Second || value > 144*time.Second {
		t.Fatalf("jitter = %s, outside 20%% bound", value)
	}
}

func TestAllAIRetryDelaysStayWithinFiveMinutes(t *testing.T) {
	for _, schedule := range []func(int) time.Duration{transientRetryDelay, configurationRetryDelay, contentRetryDelay} {
		for attempt := 0; attempt <= 100; attempt++ {
			for jobID := int64(1); jobID <= 41; jobID++ {
				delay := jitter(schedule(attempt), jobID, attempt)
				if delay <= 0 || delay > 5*time.Minute {
					t.Fatalf("attempt %d job %d: retry delay %s exceeds five-minute cap", attempt, jobID, delay)
				}
			}
		}
	}
}

func TestScheduleSupportsOvernightWindowsAndImmediateMode(t *testing.T) {
	location, err := time.LoadLocation("Europe/Berlin")
	if err != nil {
		t.Fatal(err)
	}
	schedule := Schedule{Location: location, Start: 22 * time.Hour, End: 6 * time.Hour}
	for _, test := range []struct {
		hour int
		want bool
	}{
		{hour: 21, want: false},
		{hour: 22, want: true},
		{hour: 5, want: true},
		{hour: 6, want: false},
	} {
		value := time.Date(2026, time.August, 23, test.hour, 0, 0, 0, location)
		if got := schedule.Allows(value); got != test.want {
			t.Errorf("Allows(%02d:00) = %t, want %t", test.hour, got, test.want)
		}
	}
	if !(Schedule{Immediate: true}).Allows(time.Time{}) {
		t.Fatal("immediate schedule blocked processing")
	}
}
