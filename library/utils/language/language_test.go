package language

import "testing"

func TestNormalizeLanguageTitleCasesScriptSubtag(t *testing.T) {
	got := NormalizeLanguage("ZH_hant_tw")
	if got != "zh-Hant-TW" {
		t.Fatalf("NormalizeLanguage returned %q, want zh-Hant-TW", got)
	}
}
