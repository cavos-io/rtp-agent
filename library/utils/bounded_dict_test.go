package utils

import "testing"

type boundedDictValue struct {
	Name  string
	Count int
}

func TestBoundedDictEvictsOldestInsertedItem(t *testing.T) {
	dict := NewBoundedDict[string, int](2)
	dict.Set("first", 1)
	dict.Set("second", 2)

	if _, ok := dict.Get("first"); !ok {
		t.Fatal("first item missing before capacity overflow")
	}
	dict.Set("third", 3)

	if _, ok := dict.Get("first"); ok {
		t.Fatal("first item remained after capacity overflow, want insertion-order eviction")
	}
	if got, ok := dict.Get("second"); !ok || got != 2 {
		t.Fatalf("second item = %v, %v; want 2, true", got, ok)
	}
	if got, ok := dict.Get("third"); !ok || got != 3 {
		t.Fatalf("third item = %v, %v; want 3, true", got, ok)
	}
}

func TestBoundedDictSetOrUpdateUsesFactoryOnce(t *testing.T) {
	dict := NewBoundedDict[string, boundedDictValue](2)
	factoryCalls := 0

	first := dict.SetOrUpdate("key", func() boundedDictValue {
		factoryCalls++
		return boundedDictValue{Name: "new"}
	}, func(value boundedDictValue) boundedDictValue {
		value.Count = 1
		return value
	})
	second := dict.SetOrUpdate("key", func() boundedDictValue {
		factoryCalls++
		return boundedDictValue{Name: "unexpected"}
	}, func(value boundedDictValue) boundedDictValue {
		value.Count = 2
		return value
	})

	if factoryCalls != 1 {
		t.Fatalf("factory calls = %d, want 1", factoryCalls)
	}
	if first.Name != "new" || first.Count != 1 {
		t.Fatalf("first value = %#v, want name new count 1", first)
	}
	if second.Name != "new" || second.Count != 2 {
		t.Fatalf("second value = %#v, want updated existing value", second)
	}
}

func TestBoundedDictSetOrUpdateTreatsNilPointerAsMissing(t *testing.T) {
	dict := NewBoundedDict[string, *boundedDictValue](2)
	dict.Set("key", nil)
	factoryCalls := 0

	got := dict.SetOrUpdate("key", func() *boundedDictValue {
		factoryCalls++
		return &boundedDictValue{Name: "fresh"}
	}, func(value *boundedDictValue) *boundedDictValue {
		value.Count = 1
		return value
	})

	if factoryCalls != 1 {
		t.Fatalf("factory calls = %d, want 1", factoryCalls)
	}
	if got == nil || got.Name != "fresh" || got.Count != 1 {
		t.Fatalf("SetOrUpdate(nil existing) = %#v, want fresh updated value", got)
	}
	stored, ok := dict.Get("key")
	if !ok || stored != got {
		t.Fatalf("stored value = %#v, %v; want returned fresh value", stored, ok)
	}
}

func TestBoundedDictUpdateValueOnlyUpdatesExistingKeys(t *testing.T) {
	dict := NewBoundedDict[string, boundedDictValue](2)

	if _, ok := dict.UpdateValue("missing", func(value boundedDictValue) boundedDictValue {
		value.Count = 1
		return value
	}); ok {
		t.Fatal("UpdateValue(missing) ok = true, want false")
	}

	dict.Set("key", boundedDictValue{Name: "existing"})
	got, ok := dict.UpdateValue("key", func(value boundedDictValue) boundedDictValue {
		value.Count = 3
		return value
	})
	if !ok {
		t.Fatal("UpdateValue(existing) ok = false, want true")
	}
	if got.Name != "existing" || got.Count != 3 {
		t.Fatalf("UpdateValue(existing) = %#v, want updated count only", got)
	}
}

func TestBoundedDictPopIfMatchesReferenceOrder(t *testing.T) {
	dict := NewBoundedDict[string, int](4)
	dict.Set("oldest", 1)
	dict.Set("middle", 2)
	dict.Set("newest", 3)

	key, value, ok := dict.PopIf(func(value int) bool {
		return value%2 == 1
	})
	if !ok || key != "newest" || value != 3 {
		t.Fatalf("PopIf(predicate) = %q, %d, %v; want newest odd item", key, value, ok)
	}

	key, value, ok = dict.PopIf(nil)
	if !ok || key != "oldest" || value != 1 {
		t.Fatalf("PopIf(nil) = %q, %d, %v; want oldest remaining item", key, value, ok)
	}
}

func TestBoundedDictRejectsInvalidMaxSize(t *testing.T) {
	defer func() {
		if recover() == nil {
			t.Fatal("NewBoundedDict(0) did not panic")
		}
	}()

	_ = NewBoundedDict[string, int](0)
}
