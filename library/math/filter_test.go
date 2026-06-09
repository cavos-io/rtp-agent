package math

import "testing"

func TestExpFilterAppliesReferenceInitialAndMinimum(t *testing.T) {
	initial := 10.0
	minVal := 6.0
	filter, err := NewExpFilterWithOptions(0.5, ExpFilterOptions{
		Initial: &initial,
		MinVal:  &minVal,
	})
	if err != nil {
		t.Fatalf("NewExpFilterWithOptions() error = %v", err)
	}

	got := filter.Apply(1, 2)

	if got != 6 {
		t.Fatalf("Apply() = %v, want clamped minimum 6", got)
	}
	value, ok := filter.Value()
	if !ok {
		t.Fatal("Value() ok = false, want true")
	}
	if value != 6 {
		t.Fatalf("Value() = %v, want 6", value)
	}
}

func TestExpFilterRejectsInvalidAlpha(t *testing.T) {
	if _, err := NewExpFilterWithOptions(0, ExpFilterOptions{}); err == nil {
		t.Fatal("NewExpFilterWithOptions() error = nil, want invalid alpha error")
	}
	if _, err := NewExpFilterWithOptions(1.1, ExpFilterOptions{}); err == nil {
		t.Fatal("NewExpFilterWithOptions() error = nil, want invalid alpha error")
	}
}

func TestExpFilterRequiresSampleOrInitial(t *testing.T) {
	filter, err := NewExpFilterWithOptions(0.5, ExpFilterOptions{})
	if err != nil {
		t.Fatalf("NewExpFilterWithOptions() error = %v", err)
	}

	if _, err := filter.ApplyWithoutSample(1); err == nil {
		t.Fatal("ApplyWithoutSample() error = nil, want missing sample error")
	}
}

func TestExpFilterResetAlphaPreservesReferenceValue(t *testing.T) {
	filter, err := NewExpFilterWithOptions(0.5, ExpFilterOptions{})
	if err != nil {
		t.Fatalf("NewExpFilterWithOptions() error = %v", err)
	}
	if got := filter.Apply(1, 10); got != 10 {
		t.Fatalf("initial Apply() = %v, want 10", got)
	}

	filter.Reset(0.25)

	value, ok := filter.Value()
	if !ok {
		t.Fatal("Value() ok = false after Reset(alpha), want true")
	}
	if value != 10 {
		t.Fatalf("Value() after Reset(alpha) = %v, want preserved value 10", value)
	}
	if got := filter.Apply(1, 14); got != 13 {
		t.Fatalf("Apply() after Reset(alpha) = %v, want 13", got)
	}
}

func TestExpFilterUpdateBaseUsesReferenceAssignment(t *testing.T) {
	initial := 10.0
	filter, err := NewExpFilterWithOptions(0.5, ExpFilterOptions{Initial: &initial})
	if err != nil {
		t.Fatalf("NewExpFilterWithOptions() error = %v", err)
	}

	filter.UpdateBase(2)

	if got := filter.Apply(1, 14); got != 6 {
		t.Fatalf("Apply() after UpdateBase(2) = %v, want reference assignment result 6", got)
	}
}

func TestExpFilterLegacyConstructorKeepsMaximumClamp(t *testing.T) {
	filter := NewExpFilter(0.5, 5)

	if got := filter.Apply(1, 10); got != 5 {
		t.Fatalf("Apply() = %v, want clamped maximum 5", got)
	}
	if got := filter.Filtered(); got != 5 {
		t.Fatalf("Filtered() = %v, want 5", got)
	}

	filter.Reset(-1)
	if _, ok := filter.Value(); ok {
		t.Fatal("Value() ok = true after Reset, want false")
	}
	if got := filter.Filtered(); got != -1 {
		t.Fatalf("Filtered() after Reset = %v, want legacy unset sentinel -1", got)
	}
}

func TestMovingAverageMatchesReferenceWindow(t *testing.T) {
	average := NewMovingAverage(3)

	if got := average.GetAvg(); got != 0 {
		t.Fatalf("initial GetAvg() = %v, want 0", got)
	}

	average.AddSample(1)
	if got := average.GetAvg(); got != 1 {
		t.Fatalf("GetAvg() after 1 sample = %v, want 1", got)
	}
	if got := average.Size(); got != 1 {
		t.Fatalf("Size() after 1 sample = %v, want 1", got)
	}

	average.AddSample(2)
	average.AddSample(3)
	if got := average.GetAvg(); got != 2 {
		t.Fatalf("GetAvg() after full window = %v, want 2", got)
	}
	if got := average.Size(); got != 3 {
		t.Fatalf("Size() after full window = %v, want 3", got)
	}

	average.AddSample(4)
	if got := average.GetAvg(); got != 3 {
		t.Fatalf("GetAvg() after rollover = %v, want 3", got)
	}
	if got := average.Size(); got != 3 {
		t.Fatalf("Size() after rollover = %v, want 3", got)
	}

	average.Reset()
	if got := average.GetAvg(); got != 0 {
		t.Fatalf("GetAvg() after Reset() = %v, want 0", got)
	}
	if got := average.Size(); got != 0 {
		t.Fatalf("Size() after Reset() = %v, want 0", got)
	}
}
