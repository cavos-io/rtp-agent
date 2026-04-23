package agent

import (
	"testing"
)

func TestTagger(t *testing.T) {
	tagger := NewTagger()
	
	tagger.Add("custom-tag")
	tagger.Success("mission accomplished")
	
	tags := tagger.Tags()
	foundSuccess := false
	foundCustom := false
	for _, tag := range tags {
		if tag == "lk.success" {
			foundSuccess = true
		}
		if tag == "custom-tag" {
			foundCustom = true
		}
	}
	
	if !foundSuccess || !foundCustom {
		t.Errorf("Expected tags missing. Found: %v", tags)
	}
	if tagger.OutcomeReason() != "mission accomplished" {
		t.Errorf("Expected reason mission accomplished, got %s", tagger.OutcomeReason())
	}
	
	tagger.Fail("it failed")
	tags = tagger.Tags()
	foundFail := false
	foundSuccess = false
	for _, tag := range tags {
		if tag == "lk.fail" {
			foundFail = true
		}
		if tag == "lk.success" {
			foundSuccess = true
		}
	}
	if !foundFail || foundSuccess {
		t.Errorf("Success tag should be removed and Fail tag added. Found: %v", tags)
	}
	
	tagger.Remove("custom-tag")
	if len(tagger.Tags()) != 1 { // only lk.fail
		t.Errorf("Expected 1 tag after removal, got %d", len(tagger.Tags()))
	}
	
	tagger.Evaluation(&EvaluationResult{
		Judgments: map[string]string{"clarity": "pass"},
	})
	
	foundJudge := false
	for _, tag := range tagger.Tags() {
		if tag == "lk.judge.clarity:pass" {
			foundJudge = true
		}
	}
	if !foundJudge {
		t.Error("Judge tag not found after Evaluation")
	}
}
