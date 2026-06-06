package language

import "testing"

func TestNormalizeLanguageTitleCasesScriptSubtag(t *testing.T) {
	got := NormalizeLanguage("ZH_hant_tw")
	if got != "zh-Hant-TW" {
		t.Fatalf("NormalizeLanguage returned %q, want zh-Hant-TW", got)
	}
}

func TestLanguageCodeAccessorsMatchReference(t *testing.T) {
	code := NormalizeLanguage("cmn-Hans-CN")

	if got := Language(code); got != "zh" {
		t.Fatalf("Language(%q) = %q, want zh", code, got)
	}
	if got := ISO(code); got != "zh-CN" {
		t.Fatalf("ISO(%q) = %q, want zh-CN", code, got)
	}
	if got := Region(code); got != "CN" {
		t.Fatalf("Region(%q) = %q, want CN", code, got)
	}
	if got := ToLanguageName(code); got != "chinese" {
		t.Fatalf("ToLanguageName(%q) = %q, want chinese", code, got)
	}
}

func TestLanguageCodeAccessorsHandleUnknownReferenceValues(t *testing.T) {
	code := NormalizeLanguage("multi")

	if got := Language(code); got != "multi" {
		t.Fatalf("Language(%q) = %q, want multi", code, got)
	}
	if got := ISO(code); got != "multi" {
		t.Fatalf("ISO(%q) = %q, want multi", code, got)
	}
	if got := Region(code); got != "" {
		t.Fatalf("Region(%q) = %q, want empty", code, got)
	}
	if got := ToLanguageName(code); got != "" {
		t.Fatalf("ToLanguageName(%q) = %q, want empty", code, got)
	}
}
