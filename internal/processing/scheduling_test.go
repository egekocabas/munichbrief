package processing

import (
	"testing"
	"time"
)

func TestRetrySchedulesAndJitterBounds(t *testing.T) {
	if transientRetryDelay(1) != 30*time.Second || transientRetryDelay(99) != 2*time.Hour {
		t.Fatal("transient retry schedule is incorrect")
	}
	if configurationRetryDelay(1) != 10*time.Minute || contentRetryDelay(3) != time.Hour {
		t.Fatal("configuration or content retry schedule is incorrect")
	}
	base := 10 * time.Minute
	value := jitter(base, 42, 3)
	if value < 8*time.Minute || value > 12*time.Minute {
		t.Fatalf("jitter = %s, outside 20%% bound", value)
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
