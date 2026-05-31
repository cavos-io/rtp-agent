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
