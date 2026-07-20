package workflows

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/cavos-io/rtp-agent/core/agent"
	"github.com/cavos-io/rtp-agent/core/llm"
)

func TestGetNameTaskUpdatesRequiredPartsWithoutConfirmation(t *testing.T) {
	task := NewGetNameTask(GetNameOptions{FirstName: true, LastName: true, RequireConfirmationSet: true})
	tool := &updateNameTool{task: task}

	out, err := tool.Execute(context.Background(), `{"first_name":" Ada ","last_name":" Lovelace "}`)
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	// Reference returns None after no-confirm completion, avoiding extra post-completion tool chatter.
	if out != "" {
		t.Fatalf("Execute() output = %q, want empty output after no-confirm completion", out)
	}

	select {
	case result := <-task.Result:
		if result.FirstName != "Ada" || result.LastName != "Lovelace" || result.MiddleName != "" {
			t.Fatalf("result = %#v, want Ada Lovelace", result)
		}
	default:
		t.Fatal("task did not complete after valid name")
	}
}

func TestGetNameTaskCapitalizesDirectNameParts(t *testing.T) {
	task := NewGetNameTask(GetNameOptions{FirstName: true, LastName: true, RequireConfirmationSet: true})
	tool := &updateNameTool{task: task}

	out, err := tool.Execute(context.Background(), `{"first_name":"ada","last_name":"lovelace"}`)
	if err != nil {
		t.Fatalf("Execute() error = %v, want lower-case direct name accepted", err)
	}
	if out != "" {
		t.Fatalf("Execute() output = %q, want empty output after no-confirm completion", out)
	}

	select {
	case result := <-task.Result:
		if result.FirstName != "Ada" || result.LastName != "Lovelace" {
			t.Fatalf("result = %#v, want direct name parts capitalized to Ada Lovelace", result)
		}
	default:
		t.Fatal("task did not complete after lower-case direct name")
	}
}

func TestGetNameTaskCapitalizesDirectNamePartsAfterSeparators(t *testing.T) {
	task := NewGetNameTask(GetNameOptions{FirstName: true, LastName: true, RequireConfirmationSet: true})
	tool := &updateNameTool{task: task}

	out, err := tool.Execute(context.Background(), `{"first_name":"mary-jane","last_name":"o'brien"}`)
	if err != nil {
		t.Fatalf("Execute() error = %v, want lower-case separated names accepted", err)
	}
	if out != "" {
		t.Fatalf("Execute() output = %q, want empty output after no-confirm completion", out)
	}

	select {
	case result := <-task.Result:
		if result.FirstName != "Mary-Jane" || result.LastName != "O'Brien" {
			t.Fatalf("result = %#v, want separated name parts capitalized to Mary-Jane O'Brien", result)
		}
	default:
		t.Fatal("task did not complete after lower-case separated name")
	}
}

func TestGetNameTaskCapitalizesDirectMultiwordNameParts(t *testing.T) {
	task := NewGetNameTask(GetNameOptions{FirstName: true, LastName: true, RequireConfirmationSet: true})
	tool := &updateNameTool{task: task}

	out, err := tool.Execute(context.Background(), `{"first_name":"mary ann","last_name":"van buren"}`)
	if err != nil {
		t.Fatalf("Execute() error = %v, want multiword direct name accepted", err)
	}
	if out != "" {
		t.Fatalf("Execute() output = %q, want empty output after no-confirm completion", out)
	}

	select {
	case result := <-task.Result:
		if result.FirstName != "Mary Ann" || result.LastName != "Van Buren" {
			t.Fatalf("result = %#v, want multiword name parts capitalized to Mary Ann Van Buren", result)
		}
	default:
		t.Fatal("task did not complete after multiword direct name")
	}
}

func TestGetNameTaskNormalizesSpelledLetters(t *testing.T) {
	task := NewGetNameTask(GetNameOptions{FirstName: true, LastName: true, RequireConfirmationSet: true})
	tool := &updateNameTool{task: task}

	out, err := tool.Execute(context.Background(), `{"first_name":"A d a","last_name":"L o v e l a c e"}`)
	if err != nil {
		t.Fatalf("Execute() error = %v, want spelled letters accepted", err)
	}
	if out != "" {
		t.Fatalf("Execute() output = %q, want empty output after no-confirm completion", out)
	}

	select {
	case result := <-task.Result:
		if result.FirstName != "Ada" || result.LastName != "Lovelace" {
			t.Fatalf("result = %#v, want spelled letters normalized to Ada Lovelace", result)
		}
	default:
		t.Fatal("task did not complete after spelled-letter name")
	}
}

func TestGetNameTaskNormalizesSpelledLetterAliases(t *testing.T) {
	task := NewGetNameTask(GetNameOptions{FirstName: true, LastName: true, RequireConfirmationSet: true})
	tool := &updateNameTool{task: task}

	out, err := tool.Execute(context.Background(), `{"first_name":"ay dee ay","last_name":"el oh vee ee el ay cee ee"}`)
	if err != nil {
		t.Fatalf("Execute() error = %v, want spelled letter aliases accepted", err)
	}
	if out != "" {
		t.Fatalf("Execute() output = %q, want empty output after no-confirm completion", out)
	}

	select {
	case result := <-task.Result:
		if result.FirstName != "Ada" || result.LastName != "Lovelace" {
			t.Fatalf("result = %#v, want letter aliases normalized to Ada Lovelace", result)
		}
	default:
		t.Fatal("task did not complete after spelled-letter aliases")
	}
}

func TestGetNameTaskNormalizesNameFollowedBySpelling(t *testing.T) {
	task := NewGetNameTask(GetNameOptions{FirstName: true, LastName: true, RequireConfirmationSet: true})
	tool := &updateNameTool{task: task}

	out, err := tool.Execute(context.Background(), `{"first_name":"Michael m i c h a e l","last_name":"Lovelace l o v e l a c e"}`)
	if err != nil {
		t.Fatalf("Execute() error = %v, want name followed by spelling accepted", err)
	}
	if out != "" {
		t.Fatalf("Execute() output = %q, want empty output after no-confirm completion", out)
	}

	select {
	case result := <-task.Result:
		if result.FirstName != "Michael" || result.LastName != "Lovelace" {
			t.Fatalf("result = %#v, want name-spelling phrases normalized to Michael Lovelace", result)
		}
	default:
		t.Fatal("task did not complete after name followed by spelling")
	}
}

func TestGetNameTaskNormalizesSpelledPreamble(t *testing.T) {
	task := NewGetNameTask(GetNameOptions{FirstName: true, LastName: true, RequireConfirmationSet: true})
	tool := &updateNameTool{task: task}

	out, err := tool.Execute(context.Background(), `{"first_name":"spelled Ada a d a","last_name":"spelled Lovelace l o v e l a c e"}`)
	if err != nil {
		t.Fatalf("Execute() error = %v, want spelled preamble accepted", err)
	}
	if out != "" {
		t.Fatalf("Execute() output = %q, want empty output after no-confirm completion", out)
	}

	select {
	case result := <-task.Result:
		if result.FirstName != "Ada" || result.LastName != "Lovelace" {
			t.Fatalf("result = %#v, want spelling preamble normalized to Ada Lovelace", result)
		}
	default:
		t.Fatal("task did not complete after spelling-preamble name")
	}
}

func TestGetNameTaskNormalizesPunctuatedSpelledLetters(t *testing.T) {
	task := NewGetNameTask(GetNameOptions{FirstName: true, LastName: true, RequireConfirmationSet: true})
	tool := &updateNameTool{task: task}

	out, err := tool.Execute(context.Background(), `{"first_name":"A, d a.","last_name":"L, o v e l a c e."}`)
	if err != nil {
		t.Fatalf("Execute() error = %v, want punctuated spelled letters accepted", err)
	}
	if out != "" {
		t.Fatalf("Execute() output = %q, want empty output after no-confirm completion", out)
	}

	select {
	case result := <-task.Result:
		if result.FirstName != "Ada" || result.LastName != "Lovelace" {
			t.Fatalf("result = %#v, want punctuated spelled letters normalized to Ada Lovelace", result)
		}
	default:
		t.Fatal("task did not complete after punctuated spelled-letter name")
	}
}

func TestGetNameTaskNormalizesSpelledDoubleLetters(t *testing.T) {
	task := NewGetNameTask(GetNameOptions{FirstName: true, LastName: true, RequireConfirmationSet: true})
	tool := &updateNameTool{task: task}

	out, err := tool.Execute(context.Background(), `{"first_name":"W i double l","last_name":"S m i t h"}`)
	if err != nil {
		t.Fatalf("Execute() error = %v, want spelled double letters accepted", err)
	}
	if out != "" {
		t.Fatalf("Execute() output = %q, want empty output after no-confirm completion", out)
	}

	select {
	case result := <-task.Result:
		if result.FirstName != "Will" || result.LastName != "Smith" {
			t.Fatalf("result = %#v, want spelled double letters normalized to Will Smith", result)
		}
	default:
		t.Fatal("task did not complete after spelled-double-letter name")
	}
}

func TestGetNameTaskNormalizesSpokenDoubleYouLetter(t *testing.T) {
	task := NewGetNameTask(GetNameOptions{FirstName: true, LastName: true, RequireConfirmationSet: true})
	tool := &updateNameTool{task: task}

	out, err := tool.Execute(context.Background(), `{"first_name":"Will double you i double l","last_name":"Wong double you o n g"}`)
	if err != nil {
		t.Fatalf("Execute() error = %v, want spoken double-you W accepted", err)
	}
	if out != "" {
		t.Fatalf("Execute() output = %q, want empty output after no-confirm completion", out)
	}

	select {
	case result := <-task.Result:
		if result.FirstName != "Will" || result.LastName != "Wong" {
			t.Fatalf("result = %#v, want double-you spelling normalized to Will Wong", result)
		}
	default:
		t.Fatal("task did not complete after spoken double-you spelling")
	}
}

func TestGetNameTaskNormalizesSpokenDoubleEweLetter(t *testing.T) {
	task := NewGetNameTask(GetNameOptions{FirstName: true, LastName: true, RequireConfirmationSet: true})
	tool := &updateNameTool{task: task}

	out, err := tool.Execute(context.Background(), `{"first_name":"Will double ewe i double l","last_name":"Wong double ewe o n g"}`)
	if err != nil {
		t.Fatalf("Execute() error = %v, want spoken double-ewe W accepted", err)
	}
	if out != "" {
		t.Fatalf("Execute() output = %q, want empty output after no-confirm completion", out)
	}

	select {
	case result := <-task.Result:
		if result.FirstName != "Will" || result.LastName != "Wong" {
			t.Fatalf("result = %#v, want double-ewe spelling normalized to Will Wong", result)
		}
	default:
		t.Fatal("task did not complete after spoken double-ewe spelling")
	}
}

func TestGetNameTaskNormalizesSpelledSingleLetters(t *testing.T) {
	task := NewGetNameTask(GetNameOptions{FirstName: true, LastName: true, RequireConfirmationSet: true})
	tool := &updateNameTool{task: task}

	out, err := tool.Execute(context.Background(), `{"first_name":"A single d a","last_name":"L o v e l a c e"}`)
	if err != nil {
		t.Fatalf("Execute() error = %v, want spelled single letters accepted", err)
	}
	if out != "" {
		t.Fatalf("Execute() output = %q, want empty output after no-confirm completion", out)
	}

	select {
	case result := <-task.Result:
		if result.FirstName != "Ada" || result.LastName != "Lovelace" {
			t.Fatalf("result = %#v, want spelled single letters normalized to Ada Lovelace", result)
		}
	default:
		t.Fatal("task did not complete after spelled-single-letter name")
	}
}

func TestGetNameTaskNormalizesSpelledQuadrupleLetters(t *testing.T) {
	task := NewGetNameTask(GetNameOptions{FirstName: true, LastName: true, RequireConfirmationSet: true})
	tool := &updateNameTool{task: task}

	out, err := tool.Execute(context.Background(), `{"first_name":"A quadruple l e n","last_name":"S m i t h"}`)
	if err != nil {
		t.Fatalf("Execute() error = %v, want spelled quadruple letters accepted", err)
	}
	if out != "" {
		t.Fatalf("Execute() output = %q, want empty output after no-confirm completion", out)
	}

	select {
	case result := <-task.Result:
		if result.FirstName != "Allllen" || result.LastName != "Smith" {
			t.Fatalf("result = %#v, want spelled quadruple letters normalized to Allllen Smith", result)
		}
	default:
		t.Fatal("task did not complete after spelled-quadruple-letter name")
	}
}

func TestGetNameTaskNormalizesPhoneticAlphabetSpelling(t *testing.T) {
	task := NewGetNameTask(GetNameOptions{FirstName: true, LastName: true, RequireConfirmationSet: true})
	tool := &updateNameTool{task: task}

	out, err := tool.Execute(context.Background(), `{"first_name":"Mike as in Mike India Charlie Hotel Alpha Echo Lima","last_name":"Lovelace"}`)
	if err != nil {
		t.Fatalf("Execute() error = %v, want phonetic alphabet spelling accepted", err)
	}
	if out != "" {
		t.Fatalf("Execute() output = %q, want empty output after no-confirm completion", out)
	}

	select {
	case result := <-task.Result:
		if result.FirstName != "Michael" || result.LastName != "Lovelace" {
			t.Fatalf("result = %#v, want phonetic alphabet spelling normalized to Michael Lovelace", result)
		}
	default:
		t.Fatal("task did not complete after phonetic alphabet name")
	}
}

func TestGetNameTaskNormalizesSplitXRayPhoneticSpelling(t *testing.T) {
	task := NewGetNameTask(GetNameOptions{FirstName: true, LastName: true, RequireConfirmationSet: true})
	tool := &updateNameTool{task: task}

	out, err := tool.Execute(context.Background(), `{"first_name":"Xavier as in X ray Alfa Victor India Echo Romeo","last_name":"Lovelace"}`)
	if err != nil {
		t.Fatalf("Execute() error = %v, want split X ray phonetic spelling accepted", err)
	}
	if out != "" {
		t.Fatalf("Execute() output = %q, want empty output after no-confirm completion", out)
	}

	select {
	case result := <-task.Result:
		if result.FirstName != "Xavier" || result.LastName != "Lovelace" {
			t.Fatalf("result = %#v, want split X ray phonetic spelling normalized to Xavier Lovelace", result)
		}
	default:
		t.Fatal("task did not complete after split X ray phonetic name")
	}
}

func TestGetNameTaskFiltersSpokenFiller(t *testing.T) {
	task := NewGetNameTask(GetNameOptions{FirstName: true, LastName: true, RequireConfirmationSet: true})
	tool := &updateNameTool{task: task}

	out, err := tool.Execute(context.Background(), `{"first_name":"um Ada","last_name":"uh Lovelace"}`)
	if err != nil {
		t.Fatalf("Execute() error = %v, want filler words ignored", err)
	}
	if out != "" {
		t.Fatalf("Execute() output = %q, want empty output after no-confirm completion", out)
	}

	select {
	case result := <-task.Result:
		if result.FirstName != "Ada" || result.LastName != "Lovelace" {
			t.Fatalf("result = %#v, want spoken filler filtered from Ada Lovelace", result)
		}
	default:
		t.Fatal("task did not complete after filler-normalized name")
	}
}

func TestGetNameTaskFiltersLikeSpokenFiller(t *testing.T) {
	task := NewGetNameTask(GetNameOptions{FirstName: true, LastName: true, RequireConfirmationSet: true})
	tool := &updateNameTool{task: task}

	out, err := tool.Execute(context.Background(), `{"first_name":"like Ada","last_name":"like Lovelace"}`)
	if err != nil {
		t.Fatalf("Execute() error = %v, want like filler ignored", err)
	}
	if out != "" {
		t.Fatalf("Execute() output = %q, want empty output after no-confirm completion", out)
	}

	select {
	case result := <-task.Result:
		if result.FirstName != "Ada" || result.LastName != "Lovelace" {
			t.Fatalf("result = %#v, want like filler filtered from Ada Lovelace", result)
		}
	default:
		t.Fatal("task did not complete after like-filler-normalized name")
	}
}

func TestGetNameTaskFiltersActuallySpokenFiller(t *testing.T) {
	task := NewGetNameTask(GetNameOptions{FirstName: true, LastName: true, RequireConfirmationSet: true})
	tool := &updateNameTool{task: task}

	out, err := tool.Execute(context.Background(), `{"first_name":"actually Ada","last_name":"actually Lovelace"}`)
	if err != nil {
		t.Fatalf("Execute() error = %v, want correction filler ignored", err)
	}
	if out != "" {
		t.Fatalf("Execute() output = %q, want empty output after no-confirm completion", out)
	}

	select {
	case result := <-task.Result:
		if result.FirstName != "Ada" || result.LastName != "Lovelace" {
			t.Fatalf("result = %#v, want correction filler filtered from Ada Lovelace", result)
		}
	default:
		t.Fatal("task did not complete after correction-filler-normalized name")
	}
}

func TestGetNameTaskFiltersSorrySpokenFiller(t *testing.T) {
	task := NewGetNameTask(GetNameOptions{FirstName: true, LastName: true, RequireConfirmationSet: true})
	tool := &updateNameTool{task: task}

	out, err := tool.Execute(context.Background(), `{"first_name":"sorry Ada","last_name":"sorry Lovelace"}`)
	if err != nil {
		t.Fatalf("Execute() error = %v, want apology filler ignored", err)
	}
	if out != "" {
		t.Fatalf("Execute() output = %q, want empty output after no-confirm completion", out)
	}

	select {
	case result := <-task.Result:
		if result.FirstName != "Ada" || result.LastName != "Lovelace" {
			t.Fatalf("result = %#v, want apology filler filtered from Ada Lovelace", result)
		}
	default:
		t.Fatal("task did not complete after apology-filler-normalized name")
	}
}

func TestGetNameTaskFiltersTrailingCompletionPhrase(t *testing.T) {
	task := NewGetNameTask(GetNameOptions{FirstName: true, LastName: true, RequireConfirmationSet: true})
	tool := &updateNameTool{task: task}

	out, err := tool.Execute(context.Background(), `{"first_name":"Ada that's it","last_name":"Lovelace"}`)
	if err != nil {
		t.Fatalf("Execute() error = %v, want trailing completion phrase ignored", err)
	}
	if out != "" {
		t.Fatalf("Execute() output = %q, want empty output after no-confirm completion", out)
	}

	select {
	case result := <-task.Result:
		if result.FirstName != "Ada" || result.LastName != "Lovelace" {
			t.Fatalf("result = %#v, want trailing completion phrase filtered from Ada Lovelace", result)
		}
	default:
		t.Fatal("task did not complete after trailing-completion-normalized name")
	}
}

func TestGetNameTaskFiltersTrailingThatsAllPhrase(t *testing.T) {
	task := NewGetNameTask(GetNameOptions{FirstName: true, LastName: true, RequireConfirmationSet: true})
	tool := &updateNameTool{task: task}

	out, err := tool.Execute(context.Background(), `{"first_name":"Ada that's all","last_name":"Lovelace"}`)
	if err != nil {
		t.Fatalf("Execute() error = %v, want trailing that's-all phrase ignored", err)
	}
	if out != "" {
		t.Fatalf("Execute() output = %q, want empty output after no-confirm completion", out)
	}

	select {
	case result := <-task.Result:
		if result.FirstName != "Ada" || result.LastName != "Lovelace" {
			t.Fatalf("result = %#v, want trailing that's-all phrase filtered from Ada Lovelace", result)
		}
	default:
		t.Fatal("task did not complete after trailing-thats-all-normalized name")
	}
}

func TestGetNameTaskFiltersTrailingExpandedCompletionPhrase(t *testing.T) {
	task := NewGetNameTask(GetNameOptions{FirstName: true, LastName: true, RequireConfirmationSet: true})
	tool := &updateNameTool{task: task}

	out, err := tool.Execute(context.Background(), `{"first_name":"Ada that is it","last_name":"Lovelace that is all"}`)
	if err != nil {
		t.Fatalf("Execute() error = %v, want trailing expanded completion phrase ignored", err)
	}
	if out != "" {
		t.Fatalf("Execute() output = %q, want empty output after no-confirm completion", out)
	}

	select {
	case result := <-task.Result:
		if result.FirstName != "Ada" || result.LastName != "Lovelace" {
			t.Fatalf("result = %#v, want trailing expanded completion phrase filtered from Ada Lovelace", result)
		}
	default:
		t.Fatal("task did not complete after trailing-expanded-completion-normalized name")
	}
}

func TestGetNameTaskFiltersTrailingThatWillBeAllPhrase(t *testing.T) {
	task := NewGetNameTask(GetNameOptions{FirstName: true, LastName: true, RequireConfirmationSet: true})
	tool := &updateNameTool{task: task}

	out, err := tool.Execute(context.Background(), `{"first_name":"Ada that'll be it","last_name":"Lovelace that'll be all"}`)
	if err != nil {
		t.Fatalf("Execute() error = %v, want trailing that'll-be completion phrase ignored", err)
	}
	if out != "" {
		t.Fatalf("Execute() output = %q, want empty output after no-confirm completion", out)
	}

	select {
	case result := <-task.Result:
		if result.FirstName != "Ada" || result.LastName != "Lovelace" {
			t.Fatalf("result = %#v, want trailing that'll-be completion phrase filtered from Ada Lovelace", result)
		}
	default:
		t.Fatal("task did not complete after trailing-thatll-completion-normalized name")
	}
}

func TestGetNameTaskFiltersTrailingExpandedThatWillBeAllPhrase(t *testing.T) {
	task := NewGetNameTask(GetNameOptions{FirstName: true, LastName: true, RequireConfirmationSet: true})
	tool := &updateNameTool{task: task}

	out, err := tool.Execute(context.Background(), `{"first_name":"Ada that will be it","last_name":"Lovelace that will be all"}`)
	if err != nil {
		t.Fatalf("Execute() error = %v, want trailing expanded that-will-be completion phrase ignored", err)
	}
	if out != "" {
		t.Fatalf("Execute() output = %q, want empty output after no-confirm completion", out)
	}

	select {
	case result := <-task.Result:
		if result.FirstName != "Ada" || result.LastName != "Lovelace" {
			t.Fatalf("result = %#v, want trailing expanded that-will-be completion phrase filtered from Ada Lovelace", result)
		}
	default:
		t.Fatal("task did not complete after trailing-expanded-that-will-completion-normalized name")
	}
}

func TestGetNameTaskFiltersTrailingSplitThatllShortSignoffFiller(t *testing.T) {
	task := NewGetNameTask(GetNameOptions{FirstName: true, LastName: true, RequireConfirmationSet: true})
	tool := &updateNameTool{task: task}

	out, err := tool.Execute(context.Background(), `{"first_name":"Ada that ll be it","last_name":"Lovelace that ll be all"}`)
	if err != nil {
		t.Fatalf("Execute() error = %v, want split contracted trailing completion phrase ignored", err)
	}
	if out != "" {
		t.Fatalf("Execute() output = %q, want empty output after no-confirm completion", out)
	}

	select {
	case result := <-task.Result:
		if result.FirstName != "Ada" || result.LastName != "Lovelace" {
			t.Fatalf("result = %#v, want split contracted trailing completion phrase filtered from Ada Lovelace", result)
		}
	default:
		t.Fatal("task did not complete after split contracted trailing completion phrase")
	}
}

func TestGetNameTaskFiltersTrailingPoliteFiller(t *testing.T) {
	task := NewGetNameTask(GetNameOptions{FirstName: true, LastName: true, RequireConfirmationSet: true})
	tool := &updateNameTool{task: task}

	out, err := tool.Execute(context.Background(), `{"first_name":"Ada please","last_name":"Lovelace"}`)
	if err != nil {
		t.Fatalf("Execute() error = %v, want trailing polite filler ignored", err)
	}
	if out != "" {
		t.Fatalf("Execute() output = %q, want empty output after no-confirm completion", out)
	}

	select {
	case result := <-task.Result:
		if result.FirstName != "Ada" || result.LastName != "Lovelace" {
			t.Fatalf("result = %#v, want trailing polite filler filtered from Ada Lovelace", result)
		}
	default:
		t.Fatal("task did not complete after trailing-polite-normalized name")
	}
}

func TestGetNameTaskFiltersTrailingDoneFiller(t *testing.T) {
	task := NewGetNameTask(GetNameOptions{FirstName: true, LastName: true, RequireConfirmationSet: true})
	tool := &updateNameTool{task: task}

	out, err := tool.Execute(context.Background(), `{"first_name":"Ada done","last_name":"Lovelace done"}`)
	if err != nil {
		t.Fatalf("Execute() error = %v, want trailing done filler ignored", err)
	}
	if out != "" {
		t.Fatalf("Execute() output = %q, want empty output after no-confirm completion", out)
	}

	select {
	case result := <-task.Result:
		if result.FirstName != "Ada" || result.LastName != "Lovelace" {
			t.Fatalf("result = %#v, want trailing done filler filtered from Ada Lovelace", result)
		}
	default:
		t.Fatal("task did not complete after trailing-done-normalized name")
	}
}

func TestGetNameTaskFiltersTrailingAllDoneFiller(t *testing.T) {
	task := NewGetNameTask(GetNameOptions{FirstName: true, LastName: true, RequireConfirmationSet: true})
	tool := &updateNameTool{task: task}

	out, err := tool.Execute(context.Background(), `{"first_name":"Ada all done","last_name":"Lovelace all done"}`)
	if err != nil {
		t.Fatalf("Execute() error = %v, want trailing all-done filler ignored", err)
	}
	if out != "" {
		t.Fatalf("Execute() output = %q, want empty output after no-confirm completion", out)
	}

	select {
	case result := <-task.Result:
		if result.FirstName != "Ada" || result.LastName != "Lovelace" {
			t.Fatalf("result = %#v, want trailing all-done filler filtered from Ada Lovelace", result)
		}
	default:
		t.Fatal("task did not complete after trailing-all-done-normalized name")
	}
}

func TestGetNameTaskFiltersTrailingForNowThanksSignoffFiller(t *testing.T) {
	task := NewGetNameTask(GetNameOptions{FirstName: true, LastName: true, RequireConfirmationSet: true})
	tool := &updateNameTool{task: task}

	out, err := tool.Execute(context.Background(), `{"first_name":"Ada that's it for now thanks","last_name":"Lovelace that's all for now thanks"}`)
	if err != nil {
		t.Fatalf("Execute() error = %v, want trailing for-now-thanks sign-off ignored", err)
	}
	if out != "" {
		t.Fatalf("Execute() output = %q, want empty output after no-confirm completion", out)
	}

	select {
	case result := <-task.Result:
		if result.FirstName != "Ada" || result.LastName != "Lovelace" {
			t.Fatalf("result = %#v, want trailing for-now-thanks sign-off filtered from Ada Lovelace", result)
		}
	default:
		t.Fatal("task did not complete after trailing-for-now-thanks-normalized name")
	}
}

func TestGetNameTaskFiltersTrailingForTodayThanksSignoffFiller(t *testing.T) {
	task := NewGetNameTask(GetNameOptions{FirstName: true, LastName: true, RequireConfirmationSet: true})
	tool := &updateNameTool{task: task}

	out, err := tool.Execute(context.Background(), `{"first_name":"Ada that's it for today thanks","last_name":"Lovelace that's all for today thanks"}`)
	if err != nil {
		t.Fatalf("Execute() error = %v, want trailing for-today-thanks sign-off ignored", err)
	}
	if out != "" {
		t.Fatalf("Execute() output = %q, want empty output after no-confirm completion", out)
	}

	select {
	case result := <-task.Result:
		if result.FirstName != "Ada" || result.LastName != "Lovelace" {
			t.Fatalf("result = %#v, want trailing for-today-thanks sign-off filtered from Ada Lovelace", result)
		}
	default:
		t.Fatal("task did not complete after trailing-for-today-thanks-normalized name")
	}
}

func TestGetNameTaskFiltersTrailingForYouSignoffFiller(t *testing.T) {
	task := NewGetNameTask(GetNameOptions{FirstName: true, LastName: true, RequireConfirmationSet: true})
	tool := &updateNameTool{task: task}

	out, err := tool.Execute(context.Background(), `{"first_name":"Ada that's all for you","last_name":"Lovelace that's it for you"}`)
	if err != nil {
		t.Fatalf("Execute() error = %v, want trailing for-you sign-off ignored", err)
	}
	if out != "" {
		t.Fatalf("Execute() output = %q, want empty output after no-confirm completion", out)
	}

	select {
	case result := <-task.Result:
		if result.FirstName != "Ada" || result.LastName != "Lovelace" {
			t.Fatalf("result = %#v, want trailing for-you sign-off filtered from Ada Lovelace", result)
		}
	default:
		t.Fatal("task did not complete after trailing-for-you-normalized name")
	}
}

func TestGetNameTaskFiltersTrailingShortForYouSignoffFiller(t *testing.T) {
	task := NewGetNameTask(GetNameOptions{FirstName: true, LastName: true, RequireConfirmationSet: true})
	tool := &updateNameTool{task: task}

	out, err := tool.Execute(context.Background(), `{"first_name":"Ada for you thanks","last_name":"Lovelace for you thanks"}`)
	if err != nil {
		t.Fatalf("Execute() error = %v, want trailing short for-you sign-off ignored", err)
	}
	if out != "" {
		t.Fatalf("Execute() output = %q, want empty output after no-confirm completion", out)
	}

	select {
	case result := <-task.Result:
		if result.FirstName != "Ada" || result.LastName != "Lovelace" {
			t.Fatalf("result = %#v, want trailing short for-you sign-off filtered from Ada Lovelace", result)
		}
	default:
		t.Fatal("task did not complete after trailing-short-for-you-normalized name")
	}
}

func TestGetNameTaskFiltersTrailingShortForTodaySignoffFiller(t *testing.T) {
	task := NewGetNameTask(GetNameOptions{FirstName: true, LastName: true, RequireConfirmationSet: true})
	tool := &updateNameTool{task: task}

	out, err := tool.Execute(context.Background(), `{"first_name":"Ada for today thanks","last_name":"Lovelace for today thanks"}`)
	if err != nil {
		t.Fatalf("Execute() error = %v, want trailing short for-today sign-off ignored", err)
	}
	if out != "" {
		t.Fatalf("Execute() output = %q, want empty output after no-confirm completion", out)
	}

	select {
	case result := <-task.Result:
		if result.FirstName != "Ada" || result.LastName != "Lovelace" {
			t.Fatalf("result = %#v, want trailing short for-today sign-off filtered from Ada Lovelace", result)
		}
	default:
		t.Fatal("task did not complete after trailing-short-for-today-normalized name")
	}
}

func TestGetNameTaskFiltersTrailingShortForTheDaySignoffFiller(t *testing.T) {
	task := NewGetNameTask(GetNameOptions{FirstName: true, LastName: true, RequireConfirmationSet: true})
	tool := &updateNameTool{task: task}

	out, err := tool.Execute(context.Background(), `{"first_name":"Ada for the day thanks","last_name":"Lovelace for the day thanks"}`)
	if err != nil {
		t.Fatalf("Execute() error = %v, want trailing short for-the-day sign-off ignored", err)
	}
	if out != "" {
		t.Fatalf("Execute() output = %q, want empty output after no-confirm completion", out)
	}

	select {
	case result := <-task.Result:
		if result.FirstName != "Ada" || result.LastName != "Lovelace" {
			t.Fatalf("result = %#v, want trailing short for-the-day sign-off filtered from Ada Lovelace", result)
		}
	default:
		t.Fatal("task did not complete after trailing-short-for-the-day-normalized name")
	}
}

func TestGetNameTaskFiltersTrailingForTheDayThanksSignoffFiller(t *testing.T) {
	task := NewGetNameTask(GetNameOptions{FirstName: true, LastName: true, RequireConfirmationSet: true})
	tool := &updateNameTool{task: task}

	out, err := tool.Execute(context.Background(), `{"first_name":"Ada that's it for the day thanks","last_name":"Lovelace that's all for the day thanks"}`)
	if err != nil {
		t.Fatalf("Execute() error = %v, want trailing for-the-day-thanks sign-off ignored", err)
	}
	if out != "" {
		t.Fatalf("Execute() output = %q, want empty output after no-confirm completion", out)
	}

	select {
	case result := <-task.Result:
		if result.FirstName != "Ada" || result.LastName != "Lovelace" {
			t.Fatalf("result = %#v, want trailing for-the-day-thanks sign-off filtered from Ada Lovelace", result)
		}
	default:
		t.Fatal("task did not complete after trailing-for-the-day-thanks-normalized name")
	}
}

func TestGetNameTaskFiltersTrailingExpandedThatWillBeAllForTheDaySignoffFiller(t *testing.T) {
	task := NewGetNameTask(GetNameOptions{FirstName: true, LastName: true, RequireConfirmationSet: true})
	tool := &updateNameTool{task: task}

	out, err := tool.Execute(context.Background(), `{"first_name":"Ada that will be all for the day thanks","last_name":"Lovelace that will be it for the day thanks"}`)
	if err != nil {
		t.Fatalf("Execute() error = %v, want expanded that-will-be for-the-day sign-off ignored", err)
	}
	if out != "" {
		t.Fatalf("Execute() output = %q, want empty output after no-confirm completion", out)
	}

	select {
	case result := <-task.Result:
		if result.FirstName != "Ada" || result.LastName != "Lovelace" {
			t.Fatalf("result = %#v, want expanded that-will-be for-the-day sign-off filtered from Ada Lovelace", result)
		}
	default:
		t.Fatal("task did not complete after expanded-that-will-be-for-the-day-normalized name")
	}
}

func TestGetNameTaskFiltersTrailingThatllBeAllForTheDaySignoffFiller(t *testing.T) {
	task := NewGetNameTask(GetNameOptions{FirstName: true, LastName: true, RequireConfirmationSet: true})
	tool := &updateNameTool{task: task}

	out, err := tool.Execute(context.Background(), `{"first_name":"Ada that'll be all for the day thanks","last_name":"Lovelace that'll be it for the day thanks"}`)
	if err != nil {
		t.Fatalf("Execute() error = %v, want contracted that'll-be for-the-day sign-off ignored", err)
	}
	if out != "" {
		t.Fatalf("Execute() output = %q, want empty output after no-confirm completion", out)
	}

	select {
	case result := <-task.Result:
		if result.FirstName != "Ada" || result.LastName != "Lovelace" {
			t.Fatalf("result = %#v, want contracted that'll-be for-the-day sign-off filtered from Ada Lovelace", result)
		}
	default:
		t.Fatal("task did not complete after contracted-thatll-be-for-the-day-normalized name")
	}
}

func TestGetNameTaskFiltersTrailingThatllBeAllForDaySignoffFiller(t *testing.T) {
	task := NewGetNameTask(GetNameOptions{FirstName: true, LastName: true, RequireConfirmationSet: true})
	tool := &updateNameTool{task: task}

	out, err := tool.Execute(context.Background(), `{"first_name":"Ada that'll be all for day thanks","last_name":"Lovelace that'll be it for day thanks"}`)
	if err != nil {
		t.Fatalf("Execute() error = %v, want contracted that'll-be for-day sign-off ignored", err)
	}
	if out != "" {
		t.Fatalf("Execute() output = %q, want empty output after no-confirm completion", out)
	}

	select {
	case result := <-task.Result:
		if result.FirstName != "Ada" || result.LastName != "Lovelace" {
			t.Fatalf("result = %#v, want contracted that'll-be for-day sign-off filtered from Ada Lovelace", result)
		}
	default:
		t.Fatal("task did not complete after contracted-thatll-be-for-day-normalized name")
	}
}

func TestGetNameTaskFiltersTrailingSplitThatllBeAllForDaySignoffFiller(t *testing.T) {
	task := NewGetNameTask(GetNameOptions{FirstName: true, LastName: true, RequireConfirmationSet: true})
	tool := &updateNameTool{task: task}

	out, err := tool.Execute(context.Background(), `{"first_name":"Ada that ll be all for day thanks","last_name":"Lovelace that ll be it for day thanks"}`)
	if err != nil {
		t.Fatalf("Execute() error = %v, want split contracted that'll-be for-day sign-off ignored", err)
	}
	if out != "" {
		t.Fatalf("Execute() output = %q, want empty output after no-confirm completion", out)
	}

	select {
	case result := <-task.Result:
		if result.FirstName != "Ada" || result.LastName != "Lovelace" {
			t.Fatalf("result = %#v, want split contracted that'll-be for-day sign-off filtered from Ada Lovelace", result)
		}
	default:
		t.Fatal("task did not complete after split-contracted-thatll-be-for-day-normalized name")
	}
}

func TestGetNameTaskFiltersSpokenNamePreamble(t *testing.T) {
	task := NewGetNameTask(GetNameOptions{FirstName: true, LastName: true, RequireConfirmationSet: true})
	tool := &updateNameTool{task: task}

	out, err := tool.Execute(context.Background(), `{"first_name":"my name is Ada","last_name":"Lovelace"}`)
	if err != nil {
		t.Fatalf("Execute() error = %v, want spoken name preamble accepted", err)
	}
	if out != "" {
		t.Fatalf("Execute() output = %q, want empty output after no-confirm completion", out)
	}

	select {
	case result := <-task.Result:
		if result.FirstName != "Ada" || result.LastName != "Lovelace" {
			t.Fatalf("result = %#v, want spoken name preamble filtered from Ada Lovelace", result)
		}
	default:
		t.Fatal("task did not complete after preamble-normalized name")
	}
}

func TestGetNameTaskFiltersArticleNamePreamble(t *testing.T) {
	task := NewGetNameTask(GetNameOptions{FirstName: true, LastName: true, RequireConfirmationSet: true})
	tool := &updateNameTool{task: task}

	_, err := tool.Execute(context.Background(), `{"first_name":"the name is Ada","last_name":"Lovelace"}`)
	if err != nil {
		t.Fatalf("Execute() error = %v, want article name preamble accepted", err)
	}

	select {
	case result := <-task.Result:
		if result.FirstName != "Ada" || result.LastName != "Lovelace" {
			t.Fatalf("result = %#v, want article name preamble filtered from Ada Lovelace", result)
		}
	default:
		t.Fatal("task did not complete after article-preamble-normalized name")
	}
}

func TestGetNameTaskFiltersFieldLabelPreambles(t *testing.T) {
	task := NewGetNameTask(GetNameOptions{FirstName: true, LastName: true, RequireConfirmationSet: true})
	tool := &updateNameTool{task: task}

	_, err := tool.Execute(context.Background(), `{"first_name":"first name is Ada","last_name":"last name is Lovelace"}`)
	if err != nil {
		t.Fatalf("Execute() error = %v, want field-label name preambles accepted", err)
	}

	select {
	case result := <-task.Result:
		if result.FirstName != "Ada" || result.LastName != "Lovelace" {
			t.Fatalf("result = %#v, want field-label preambles filtered from Ada Lovelace", result)
		}
	default:
		t.Fatal("task did not complete after field-label-preamble name")
	}
}

func TestGetNameTaskFiltersFieldLabelWillBePreambles(t *testing.T) {
	task := NewGetNameTask(GetNameOptions{FirstName: true, LastName: true, RequireConfirmationSet: true})
	tool := &updateNameTool{task: task}

	_, err := tool.Execute(context.Background(), `{"first_name":"first name will be Ada","last_name":"last name will be Lovelace"}`)
	if err != nil {
		t.Fatalf("Execute() error = %v, want field-label will-be name preambles accepted", err)
	}

	select {
	case result := <-task.Result:
		if result.FirstName != "Ada" || result.LastName != "Lovelace" {
			t.Fatalf("result = %#v, want field-label will-be preambles filtered from Ada Lovelace", result)
		}
	default:
		t.Fatal("task did not complete after field-label-will-be-preamble name")
	}
}

func TestGetNameTaskFiltersContractedFieldLabelPreambles(t *testing.T) {
	task := NewGetNameTask(GetNameOptions{FirstName: true, LastName: true, RequireConfirmationSet: true})
	tool := &updateNameTool{task: task}

	_, err := tool.Execute(context.Background(), `{"first_name":"first name's Ada","last_name":"last name's Lovelace"}`)
	if err != nil {
		t.Fatalf("Execute() error = %v, want contracted field-label preambles accepted", err)
	}

	select {
	case result := <-task.Result:
		if result.FirstName != "Ada" || result.LastName != "Lovelace" {
			t.Fatalf("result = %#v, want contracted field-label preambles filtered from Ada Lovelace", result)
		}
	default:
		t.Fatal("task did not complete after contracted field-label-preamble name")
	}
}

func TestGetNameTaskFiltersSplitContractedFieldLabelPreambles(t *testing.T) {
	task := NewGetNameTask(GetNameOptions{FirstName: true, LastName: true, RequireConfirmationSet: true})
	tool := &updateNameTool{task: task}

	_, err := tool.Execute(context.Background(), `{"first_name":"first name s Ada","last_name":"last name s Lovelace"}`)
	if err != nil {
		t.Fatalf("Execute() error = %v, want split contracted field-label preambles accepted", err)
	}

	select {
	case result := <-task.Result:
		if result.FirstName != "Ada" || result.LastName != "Lovelace" {
			t.Fatalf("result = %#v, want split contracted field-label preambles filtered from Ada Lovelace", result)
		}
	default:
		t.Fatal("task did not complete after split contracted field-label-preamble name")
	}
}

func TestGetNameTaskFiltersShortFieldLabelPreambles(t *testing.T) {
	task := NewGetNameTask(GetNameOptions{FirstName: true, LastName: true, RequireConfirmationSet: true})
	tool := &updateNameTool{task: task}

	_, err := tool.Execute(context.Background(), `{"first_name":"first is Ada","last_name":"last is Lovelace"}`)
	if err != nil {
		t.Fatalf("Execute() error = %v, want short field-label name preambles accepted", err)
	}

	select {
	case result := <-task.Result:
		if result.FirstName != "Ada" || result.LastName != "Lovelace" {
			t.Fatalf("result = %#v, want short field-label preambles filtered from Ada Lovelace", result)
		}
	default:
		t.Fatal("task did not complete after short field-label-preamble name")
	}
}

func TestGetNameTaskNormalizesSpokenSymbols(t *testing.T) {
	task := NewGetNameTask(GetNameOptions{FirstName: true, LastName: true, RequireConfirmationSet: true})
	tool := &updateNameTool{task: task}

	out, err := tool.Execute(context.Background(), `{"first_name":"Mary dash Jane","last_name":"O apostrophe Brien"}`)
	if err != nil {
		t.Fatalf("Execute() error = %v, want spoken name symbols accepted", err)
	}
	if out != "" {
		t.Fatalf("Execute() output = %q, want empty output after no-confirm completion", out)
	}

	select {
	case result := <-task.Result:
		if result.FirstName != "Mary-Jane" || result.LastName != "O'Brien" {
			t.Fatalf("result = %#v, want spoken symbols normalized", result)
		}
	default:
		t.Fatal("task did not complete after spoken-symbol name")
	}
}

func TestGetNameTaskNormalizesSpokenMinusHyphen(t *testing.T) {
	task := NewGetNameTask(GetNameOptions{FirstName: true, LastName: true, RequireConfirmationSet: true})
	tool := &updateNameTool{task: task}

	out, err := tool.Execute(context.Background(), `{"first_name":"Mary minus Jane","last_name":"O apostrophe Brien"}`)
	if err != nil {
		t.Fatalf("Execute() error = %v, want spoken minus hyphen accepted", err)
	}
	if out != "" {
		t.Fatalf("Execute() output = %q, want empty output after no-confirm completion", out)
	}

	select {
	case result := <-task.Result:
		if result.FirstName != "Mary-Jane" || result.LastName != "O'Brien" {
			t.Fatalf("result = %#v, want spoken minus normalized as hyphen", result)
		}
	default:
		t.Fatal("task did not complete after spoken-minus name")
	}
}

func TestGetNameTaskNormalizesSplitHyphen(t *testing.T) {
	task := NewGetNameTask(GetNameOptions{FirstName: true, LastName: true, RequireConfirmationSet: true})
	tool := &updateNameTool{task: task}

	out, err := tool.Execute(context.Background(), `{"first_name":"Mary hy phen Jane","last_name":"O apostrophe Brien"}`)
	if err != nil {
		t.Fatalf("Execute() error = %v, want split spoken hyphen accepted", err)
	}
	if out != "" {
		t.Fatalf("Execute() output = %q, want empty output after no-confirm completion", out)
	}

	select {
	case result := <-task.Result:
		if result.FirstName != "Mary-Jane" || result.LastName != "O'Brien" {
			t.Fatalf("result = %#v, want split spoken hyphen normalized", result)
		}
	default:
		t.Fatal("task did not complete after split spoken hyphen name")
	}
}

func TestGetNameTaskNormalizesSpokenSingleQuote(t *testing.T) {
	task := NewGetNameTask(GetNameOptions{FirstName: true, LastName: true, RequireConfirmationSet: true})
	tool := &updateNameTool{task: task}

	out, err := tool.Execute(context.Background(), `{"first_name":"Mary dash Jane","last_name":"O single quote Brien"}`)
	if err != nil {
		t.Fatalf("Execute() error = %v, want spoken single quote accepted as apostrophe", err)
	}
	if out != "" {
		t.Fatalf("Execute() output = %q, want empty output after no-confirm completion", out)
	}

	select {
	case result := <-task.Result:
		if result.FirstName != "Mary-Jane" || result.LastName != "O'Brien" {
			t.Fatalf("result = %#v, want spoken single quote normalized as apostrophe", result)
		}
	default:
		t.Fatal("task did not complete after spoken-single-quote name")
	}
}

func TestGetNameTaskNormalizesSpokenQuote(t *testing.T) {
	task := NewGetNameTask(GetNameOptions{FirstName: true, LastName: true, RequireConfirmationSet: true})
	tool := &updateNameTool{task: task}

	out, err := tool.Execute(context.Background(), `{"first_name":"Mary dash Jane","last_name":"O quote Brien"}`)
	if err != nil {
		t.Fatalf("Execute() error = %v, want spoken quote accepted as apostrophe", err)
	}
	if out != "" {
		t.Fatalf("Execute() output = %q, want empty output after no-confirm completion", out)
	}

	select {
	case result := <-task.Result:
		if result.FirstName != "Mary-Jane" || result.LastName != "O'Brien" {
			t.Fatalf("result = %#v, want spoken quote normalized as apostrophe", result)
		}
	default:
		t.Fatal("task did not complete after spoken-quote name")
	}
}

func TestGetNameTaskCapitalizesLowercaseSpokenSymbols(t *testing.T) {
	task := NewGetNameTask(GetNameOptions{FirstName: true, LastName: true, RequireConfirmationSet: true})
	tool := &updateNameTool{task: task}

	out, err := tool.Execute(context.Background(), `{"first_name":"mary dash jane","last_name":"o apostrophe brien"}`)
	if err != nil {
		t.Fatalf("Execute() error = %v, want lowercase spoken symbols accepted", err)
	}
	if out != "" {
		t.Fatalf("Execute() output = %q, want empty output after no-confirm completion", out)
	}

	select {
	case result := <-task.Result:
		if result.FirstName != "Mary-Jane" || result.LastName != "O'Brien" {
			t.Fatalf("result = %#v, want lowercase spoken symbols capitalized", result)
		}
	default:
		t.Fatal("task did not complete after lowercase spoken-symbol name")
	}
}

func TestGetNameTaskNormalizesPunctuatedSpokenSymbols(t *testing.T) {
	task := NewGetNameTask(GetNameOptions{FirstName: true, LastName: true, RequireConfirmationSet: true})
	tool := &updateNameTool{task: task}

	_, err := tool.Execute(context.Background(), `{"first_name":"Mary dash, Jane","last_name":"O apostrophe. Brien"}`)
	if err != nil {
		t.Fatalf("Execute() error = %v, want punctuated spoken name symbols accepted", err)
	}

	select {
	case result := <-task.Result:
		if result.FirstName != "Mary-Jane" || result.LastName != "O'Brien" {
			t.Fatalf("result = %#v, want punctuated spoken symbols normalized", result)
		}
	default:
		t.Fatal("task did not complete after punctuated spoken-symbol name")
	}
}

func TestGetNameTaskNormalizesSpokenSymbolSuffixes(t *testing.T) {
	task := NewGetNameTask(GetNameOptions{FirstName: true, LastName: true, RequireConfirmationSet: true})
	tool := &updateNameTool{task: task}

	_, err := tool.Execute(context.Background(), `{"first_name":"Mary dash symbol Jane","last_name":"O apostrophe symbol Brien"}`)
	if err != nil {
		t.Fatalf("Execute() error = %v, want spoken symbol suffixes accepted", err)
	}

	select {
	case result := <-task.Result:
		if result.FirstName != "Mary-Jane" || result.LastName != "O'Brien" {
			t.Fatalf("result = %#v, want spoken symbol suffixes normalized", result)
		}
	default:
		t.Fatal("task did not complete after spoken symbol suffix name")
	}
}

func TestGetNameTaskNormalizesSpokenSignSuffixes(t *testing.T) {
	task := NewGetNameTask(GetNameOptions{FirstName: true, LastName: true, RequireConfirmationSet: true})
	tool := &updateNameTool{task: task}

	_, err := tool.Execute(context.Background(), `{"first_name":"Mary dash sign Jane","last_name":"O apostrophe sign Brien"}`)
	if err != nil {
		t.Fatalf("Execute() error = %v, want spoken sign suffixes accepted", err)
	}

	select {
	case result := <-task.Result:
		if result.FirstName != "Mary-Jane" || result.LastName != "O'Brien" {
			t.Fatalf("result = %#v, want spoken sign suffixes normalized", result)
		}
	default:
		t.Fatal("task did not complete after spoken sign suffix name")
	}
}

func TestGetNameTaskNormalizesSpokenKeySuffixes(t *testing.T) {
	task := NewGetNameTask(GetNameOptions{FirstName: true, LastName: true, RequireConfirmationSet: true})
	tool := &updateNameTool{task: task}

	_, err := tool.Execute(context.Background(), `{"first_name":"Mary dash key Jane","last_name":"O apostrophe key Brien"}`)
	if err != nil {
		t.Fatalf("Execute() error = %v, want spoken key suffixes accepted", err)
	}

	select {
	case result := <-task.Result:
		if result.FirstName != "Mary-Jane" || result.LastName != "O'Brien" {
			t.Fatalf("result = %#v, want spoken key suffixes normalized", result)
		}
	default:
		t.Fatal("task did not complete after spoken key suffix name")
	}
}

func TestGetNameTaskNormalizesSpokenMarkSuffixes(t *testing.T) {
	task := NewGetNameTask(GetNameOptions{FirstName: true, LastName: true, RequireConfirmationSet: true})
	tool := &updateNameTool{task: task}

	_, err := tool.Execute(context.Background(), `{"first_name":"Mary dash mark Jane","last_name":"O apostrophe mark Brien"}`)
	if err != nil {
		t.Fatalf("Execute() error = %v, want spoken mark suffixes accepted", err)
	}

	select {
	case result := <-task.Result:
		if result.FirstName != "Mary-Jane" || result.LastName != "O'Brien" {
			t.Fatalf("result = %#v, want spoken mark suffixes normalized", result)
		}
	default:
		t.Fatal("task did not complete after spoken mark suffix name")
	}
}

func TestNewGetNameTaskRejectsEmptyNameParts(t *testing.T) {
	defer func() {
		recovered := recover()
		if recovered == nil {
			t.Fatal("NewGetNameTask() did not panic, want empty name parts rejected")
		}
		if got := recovered; got != "At least one of first_name, middle_name, or last_name must be True" {
			t.Fatalf("panic = %#v, want reference empty-parts error", got)
		}
	}()

	_ = NewGetNameTask(GetNameOptions{NamePartsSet: true})
}

func TestNewGetNameTaskDefaultsToFirstName(t *testing.T) {
	task := NewGetNameTask(GetNameOptions{})

	if !task.CollectFirstName || task.CollectMiddleName || task.CollectLastName {
		t.Fatalf("name parts = first:%t middle:%t last:%t, want default first-name capture", task.CollectFirstName, task.CollectMiddleName, task.CollectLastName)
	}
	if !strings.Contains(task.Instructions, "You need to naturally collect the name parts in this order: {first_name}.") {
		t.Fatalf("Instructions = %q, want default first-name format", task.Instructions)
	}
}

func TestGetNameTaskVerifySpellingAddsReferenceInstruction(t *testing.T) {
	task := NewGetNameTask(GetNameOptions{FirstName: true, VerifySpelling: true})

	for _, want := range []string{
		"After receiving the name, always verify the spelling by asking the user to confirm or spell out the name letter by letter.",
		"When confirming, spell out each name part letter by letter to the user.",
	} {
		if !strings.Contains(task.Instructions, want) {
			t.Fatalf("Instructions = %q, want spelling verification guidance %q", task.Instructions, want)
		}
	}
	spellingIndex := strings.Index(task.Instructions, "After receiving the name, always verify the spelling")
	updateIndex := strings.Index(task.Instructions, "Call `update_name` at the first opportunity")
	if spellingIndex < 0 || updateIndex < 0 {
		t.Fatalf("Instructions = %q, want spelling and update guidance", task.Instructions)
	}
	if spellingIndex > updateIndex {
		t.Fatalf("spelling guidance appears after update guidance in %q", task.Instructions)
	}
}

func TestGetNameTaskRejectsMissingRequiredPart(t *testing.T) {
	task := NewGetNameTask(GetNameOptions{FirstName: true, LastName: true, RequireConfirmationSet: true})
	tool := &updateNameTool{task: task}

	_, err := tool.Execute(context.Background(), `{"first_name":"Ada"}`)
	if err == nil {
		t.Fatal("Execute() error = nil, want missing last name error")
	}

	select {
	case result := <-task.Result:
		t.Fatalf("task completed with %#v, want no completion for incomplete name", result)
	default:
	}
}

func TestGetNameTaskRejectsNamePartsWithoutLetters(t *testing.T) {
	task := NewGetNameTask(GetNameOptions{FirstName: true, RequireConfirmationSet: true})
	tool := &updateNameTool{task: task}

	_, err := tool.Execute(context.Background(), `{"first_name":"12345"}`)
	if err == nil {
		t.Fatal("Execute() error = nil, want no-letter name error")
	}
	want := "Incomplete name: first name '12345' contains no letters - that doesn't look like a name"
	if err.Error() != want {
		t.Fatalf("Execute() error = %v, want %q", err, want)
	}

	select {
	case result := <-task.Result:
		t.Fatalf("task completed with %#v, want no completion for no-letter name", result)
	default:
	}
}

func TestGetNameTaskOptionsSeedReferenceChatContext(t *testing.T) {
	chatCtx := llm.NewChatContext()
	chatCtx.Append(&llm.ChatMessage{
		ID:      "prior-name",
		Role:    llm.ChatRoleUser,
		Content: []llm.ChatContent{{Text: "The guest name is Ada Lovelace."}},
	})
	opts := GetNameOptions{FirstName: true, LastName: true}
	field := reflect.ValueOf(&opts).Elem().FieldByName("ChatContext")
	if !field.IsValid() {
		t.Fatal("GetNameOptions.ChatContext missing; want reference chat_ctx constructor option")
	}
	field.Set(reflect.ValueOf(chatCtx))

	task := NewGetNameTask(opts)

	if task.ChatCtx == nil {
		t.Fatal("task ChatCtx = nil, want constructor chat context copy")
	}
	if task.ChatCtx == chatCtx {
		t.Fatal("task ChatCtx aliases constructor context, want reference-style copy")
	}
	if task.ChatCtx.GetByID("prior-name") == nil {
		t.Fatalf("task ChatCtx items = %#v, want constructor chat item", task.ChatCtx.Items)
	}
}

func TestGetNameTaskPreservesReferenceExtraTools(t *testing.T) {
	opts := GetNameOptions{FirstName: true}
	field := reflect.ValueOf(&opts).Elem().FieldByName("Tools")
	if !field.IsValid() {
		t.Fatal("GetNameOptions.Tools missing; want reference tools constructor option")
	}
	field.Set(reflect.ValueOf([]llm.Tool{referenceNameExtraTool{id: "name_help"}}))

	task := NewGetNameTask(opts)

	if len(task.Agent.Tools) < 2 {
		t.Fatalf("tools = %#v, want extra tool then update/decline tools", task.Agent.Tools)
	}
	if got := task.Agent.Tools[0].Name(); got != "name_help" {
		t.Fatalf("first tool = %q, want reference extra tool before update tool", got)
	}
	if got := task.Agent.Tools[1].Name(); got != "update_name" {
		t.Fatalf("second tool = %q, want update_name after extra tools", got)
	}
}

func TestGetNameTaskExplicitAskIgnoresUpdateToolOnEnter(t *testing.T) {
	task := NewGetNameTask(GetNameOptions{FirstName: true, RequireExplicitAsk: true})
	tool := task.Agent.Tools[0]

	if !llm.ToolHasFlag(tool, llm.ToolFlagIgnoreOnEnter) {
		t.Fatalf("%s ToolFlags missing ToolFlagIgnoreOnEnter when RequireExplicitAsk is true", tool.Name())
	}
}

func TestGetNameTaskUpdateToolUsesReferenceSchema(t *testing.T) {
	task := NewGetNameTask(GetNameOptions{FirstName: true, MiddleName: true, LastName: true})
	tool := task.Agent.Tools[0]

	wantDescription := "Update the name provided by the user."
	if got := tool.Description(); got != wantDescription {
		t.Fatalf("update_name description = %q, want %q", got, wantDescription)
	}
	properties, ok := tool.Parameters()["properties"].(map[string]any)
	if !ok {
		t.Fatalf("properties = %#v, want map", tool.Parameters()["properties"])
	}
	cases := map[string]string{
		"first_name":  "The user's first name.",
		"middle_name": "The user's middle name, if collected.",
		"last_name":   "The user's last name, if collected.",
	}
	for field, want := range cases {
		schema, ok := properties[field].(map[string]any)
		if !ok {
			t.Fatalf("%s schema = %#v, want map", field, properties[field])
		}
		if got := schema["description"]; got != want {
			t.Fatalf("%s description = %#v, want %q", field, got, want)
		}
	}
}

func TestGetNameTaskRequiresConfirmation(t *testing.T) {
	task := NewGetNameTask(GetNameOptions{FirstName: true, LastName: true})
	update := &updateNameTool{task: task}

	out, err := update.Execute(context.Background(), `{"first_name":"Ada","last_name":"Lovelace"}`)
	if err != nil {
		t.Fatalf("update Execute() error = %v", err)
	}
	want := "Ask the user to confirm the updated name without repeating it back."
	if !strings.Contains(out, want) {
		t.Fatalf("update output = %q, want confirmation guidance %q", out, want)
	}
	if strings.Contains(out, "Repeat the name back to the user") {
		t.Fatalf("update output = %q, want no name readback guidance", out)
	}
	if strings.Contains(out, "The name has been updated to Ada Lovelace") {
		t.Fatalf("update output = %q, want no raw full-name echo in update status", out)
	}
	if len(task.Agent.Tools) != 3 || task.Agent.Tools[2].Name() != "confirm_name" {
		t.Fatalf("tools = %#v, want confirm_name appended", task.Agent.Tools)
	}

	confirm := &confirmNameTool{task: task, firstName: "Ada", lastName: "Lovelace"}
	confirmOut, err := confirm.Execute(context.Background(), `{}`)
	if err != nil {
		t.Fatalf("confirm Execute() error = %v", err)
	}
	if confirmOut != "" {
		t.Fatalf("confirm Execute() output = %q, want empty output after completion", confirmOut)
	}

	select {
	case result := <-task.Result:
		if result.FirstName != "Ada" || result.LastName != "Lovelace" {
			t.Fatalf("result = %#v, want Ada Lovelace", result)
		}
	default:
		t.Fatal("task did not complete after confirmation")
	}
}

func TestConfirmNameToolUsesReferenceSchema(t *testing.T) {
	task := NewGetNameTask(GetNameOptions{FirstName: true, LastName: true})
	tool := &confirmNameTool{task: task, firstName: "Ada", lastName: "Lovelace"}

	wantDescription := "Call after the user confirms the name is correct."
	if got := tool.Description(); got != wantDescription {
		t.Fatalf("confirm_name description = %q, want %q", got, wantDescription)
	}
	properties, ok := tool.Parameters()["properties"].(map[string]any)
	if !ok {
		t.Fatalf("properties = %#v, want map", tool.Parameters()["properties"])
	}
	if len(properties) != 0 {
		t.Fatalf("properties = %#v, want empty parameter schema", properties)
	}
}

func TestGetNameTaskVerifySpellingOutputSpellsNameLetters(t *testing.T) {
	task := NewGetNameTask(GetNameOptions{FirstName: true, LastName: true, VerifySpelling: true})
	update := &updateNameTool{task: task}

	out, err := update.Execute(context.Background(), `{"first_name":"Ada","last_name":"Lovelace"}`)
	if err != nil {
		t.Fatalf("update Execute() error = %v", err)
	}
	if strings.Contains(out, "Spell out the name letter by letter for verification: Ada Lovelace") {
		t.Fatalf("update output = %q, want letter-spaced spelling guidance instead of raw name", out)
	}
	if strings.Contains(out, "The name has been updated to Ada Lovelace") {
		t.Fatalf("update output = %q, want no raw full-name echo in spelling path", out)
	}
	want := "Ask the user to confirm the updated name spelling without repeating it back."
	if !strings.Contains(out, want) {
		t.Fatalf("update output = %q, want spelling confirmation guidance %q", out, want)
	}
	if strings.Contains(out, "A d a L o v e l a c e") {
		t.Fatalf("update output = %q, want no letter-spaced name echo", out)
	}
}

func TestGetNameTaskCustomNameFormatShapesInstructionsAndOutput(t *testing.T) {
	task := NewGetNameTask(GetNameOptions{
		FirstName:  true,
		MiddleName: true,
		LastName:   true,
		NameFormat: "{last_name}, {first_name} {middle_name}",
	})
	update := &updateNameTool{task: task}

	if !strings.Contains(task.Instructions, "You need to naturally collect the name parts in this order: {last_name}, {first_name} {middle_name}.") {
		t.Fatalf("Instructions = %q, want custom name format", task.Instructions)
	}

	out, err := update.Execute(context.Background(), `{"first_name":"Ada","middle_name":"Byron","last_name":"Lovelace"}`)
	if err != nil {
		t.Fatalf("update Execute() error = %v", err)
	}
	if task.fullName() != "Lovelace, Ada Byron" {
		t.Fatalf("fullName() = %q, want custom formatted full name", task.fullName())
	}
	if !strings.Contains(out, "The name has been updated.") {
		t.Fatalf("update output = %q, want generic name update guidance", out)
	}
	if strings.Contains(out, "The name has been updated to Lovelace, Ada Byron") {
		t.Fatalf("update output = %q, want no raw custom full-name echo", out)
	}
}

func TestGetNameTaskCanDisableDefaultConfirmation(t *testing.T) {
	task := NewGetNameTask(GetNameOptions{FirstName: true, LastName: true, RequireConfirmation: false, RequireConfirmationSet: true})
	update := &updateNameTool{task: task}

	out, err := update.Execute(context.Background(), `{"first_name":"Ada","last_name":"Lovelace"}`)
	if err != nil {
		t.Fatalf("update Execute() error = %v", err)
	}
	if out != "" {
		t.Fatalf("update Execute() output = %q, want empty output after no-confirm completion", out)
	}
}

func TestGetNameTaskDefaultConfirmationUsesInputModality(t *testing.T) {
	textCtx := agent.WithRunContext(
		context.Background(),
		agent.NewRunContext(nil, agent.NewSpeechHandle(true, agent.InputDetails{Modality: "text"}), nil),
	)
	task := NewGetNameTask(GetNameOptions{FirstName: true, LastName: true})
	update := &updateNameTool{task: task}

	out, err := update.Execute(textCtx, `{"first_name":"Ada","last_name":"Lovelace"}`)
	if err != nil {
		t.Fatalf("update Execute() error = %v", err)
	}
	if out != "" {
		t.Fatalf("update Execute() output = %q, want direct text completion without confirmation prompt", out)
	}

	select {
	case result := <-task.Result:
		if result.FirstName != "Ada" || result.LastName != "Lovelace" {
			t.Fatalf("result = %#v, want direct text completion", result)
		}
	default:
		t.Fatal("task did not complete for text input")
	}
}

func TestGetNameTaskInstructionsIncludeReferenceConfirmationWhenEnabled(t *testing.T) {
	task := NewGetNameTask(GetNameOptions{FirstName: true})

	wantParts := []string{
		"You need to naturally collect the name parts in this order: {first_name}.",
		"Say their name followed by spelling: e.g., 'Michael m i c h a e l'",
		"Use phonetic alphabet: e.g., 'Mike as in Mike India Charlie Hotel Alpha Echo Lima'",
		"Convert 'dash' or 'hyphen' to `-`.",
		"Convert 'apostrophe' to `'`.",
		"Recognize when users spell out their name letter by letter.",
		"Call `update_name` at the first opportunity whenever you form a new hypothesis about the name. (before asking any questions or providing any answers.)",
		"Call `confirm_name` after the user confirmed the name is correct.",
		"If the name is unclear or it takes too much back-and-forth, prompt for each name part separately.",
		"Avoid verbosity by not sharing example names or spellings unless prompted to do so. Do not deviate from the goal of collecting the user's name.",
		"Always explicitly invoke a tool when applicable. Do not simulate tool usage, no real action is taken unless the tool is explicitly called.",
	}
	for _, want := range wantParts {
		if !strings.Contains(task.Instructions, want) {
			t.Fatalf("Instructions = %q, want reference instruction %q", task.Instructions, want)
		}
	}
	if strings.Contains(task.Instructions, "decline_name_capture") {
		t.Fatalf("Instructions = %q, want no non-reference decline tool guidance", task.Instructions)
	}
}

func TestGetNameTaskInstructionsPreserveReferenceModalityVariants(t *testing.T) {
	task := NewGetNameTask(GetNameOptions{FirstName: true})

	if task.InstructionVariants == nil {
		t.Fatal("InstructionVariants = nil, want reference audio/text instruction variants")
	}
	audio := task.InstructionVariants.AsModality("audio").String()
	text := task.InstructionVariants.AsModality("text").String()

	for _, want := range []string{
		"Handle input as noisy voice transcription.",
		"Use phonetic alphabet: e.g., 'Mike as in Mike India Charlie Hotel Alpha Echo Lima'",
		"Call `confirm_name` after the user confirmed the name is correct.",
	} {
		if !strings.Contains(audio, want) {
			t.Fatalf("audio instructions = %q, want reference audio guidance %q", audio, want)
		}
	}
	for _, want := range []string{
		"Handle input as typed text. Expect users to type their name directly.",
		"If the name contains special characters or hyphens (e.g., 'Mary-Jane' or 'O'Brien'), preserve them as typed.",
	} {
		if !strings.Contains(text, want) {
			t.Fatalf("text instructions = %q, want reference text guidance %q", text, want)
		}
	}
	for _, stale := range []string{
		"Handle input as noisy voice transcription.",
		"Use phonetic alphabet",
		"Call `confirm_name` after the user confirmed the name is correct.",
	} {
		if strings.Contains(text, stale) {
			t.Fatalf("text instructions = %q, want no audio/default-confirmation guidance %q", text, stale)
		}
	}
}

func TestGetNameTaskInstructionsOmitConfirmationWhenDisabled(t *testing.T) {
	task := NewGetNameTask(GetNameOptions{FirstName: true, RequireConfirmation: false, RequireConfirmationSet: true})

	if strings.Contains(task.Instructions, "confirm_name") {
		t.Fatalf("Instructions = %q, want no confirm_name guidance when confirmation disabled", task.Instructions)
	}
}

func TestGetNameTaskOnEnterUsesReferenceConversationScanPrompt(t *testing.T) {
	task := NewGetNameTask(GetNameOptions{FirstName: true, LastName: true})
	session := agent.NewAgentSession(task, nil, agent.AgentSessionOptions{})
	session.Assistant = &fakeDtmfSessionAssistant{}
	speechEvents := session.SpeechCreatedEvents()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	if err := session.Start(ctx); err != nil {
		t.Fatalf("Start error = %v", err)
	}
	defer session.Stop(context.Background())

	select {
	case ev := <-speechEvents:
		if ev.Source != "generate_reply" {
			t.Fatalf("SpeechCreated Source = %q, want generate_reply", ev.Source)
		}
		if ev.SpeechHandle == nil {
			t.Fatal("SpeechCreated SpeechHandle = nil, want on-enter reply handle")
		}
		if ev.SpeechHandle.Generation.Instructions == nil {
			t.Fatal("on-enter instructions = nil, want reference prompt")
		}
		got := ev.SpeechHandle.Generation.Instructions.AsModality("text").String()
		for _, want := range []string{
			"First scan the conversation - if a name was already given earlier, ask a short confirmation question rather than asking from scratch.",
			"If context about what the name is FOR was provided (a role like 'cardholder', 'guest', 'emergency contact'), anchor your confirmation question to that role",
			"Only ask fresh when the conversation has no name yet.",
		} {
			if !strings.Contains(got, want) {
				t.Fatalf("on-enter instructions = %q, want reference guidance %q", got, want)
			}
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for name on-enter prompt")
	}
}

func TestGetNameTaskStaleConfirmationPromptsForUpdatedName(t *testing.T) {
	task := NewGetNameTask(GetNameOptions{FirstName: true, LastName: true})
	session := agent.NewAgentSession(task, nil, agent.AgentSessionOptions{})
	session.Assistant = &fakeDtmfSessionAssistant{}
	speechEvents := session.SpeechCreatedEvents()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	if err := session.Start(ctx); err != nil {
		t.Fatalf("Start error = %v", err)
	}
	defer session.Stop(context.Background())

	select {
	case <-speechEvents:
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for name on-enter prompt")
	}

	update := &updateNameTool{task: task}
	if _, err := update.Execute(context.Background(), `{"first_name":"Ada","last_name":"Lovelace"}`); err != nil {
		t.Fatalf("first update Execute() error = %v", err)
	}
	staleConfirm := &confirmNameTool{task: task, firstName: "Ada", lastName: "Lovelace"}

	if _, err := update.Execute(context.Background(), `{"first_name":"Grace","last_name":"Hopper"}`); err != nil {
		t.Fatalf("second update Execute() error = %v", err)
	}
	if _, err := staleConfirm.Execute(context.Background(), `{}`); err != nil {
		t.Fatalf("stale confirm Execute() error = %v, want nil after prompting for updated confirmation", err)
	}

	select {
	case ev := <-speechEvents:
		if ev.Source != "generate_reply" {
			t.Fatalf("SpeechCreated Source = %q, want generate_reply", ev.Source)
		}
		if ev.SpeechHandle == nil {
			t.Fatal("SpeechCreated SpeechHandle = nil, want stale confirmation reply handle")
		}
		want := nameStaleConfirmationPrompt()
		if ev.SpeechHandle.Generation.Instructions == nil {
			t.Fatal("stale confirmation instructions = nil, want changed-name prompt")
		}
		if got := ev.SpeechHandle.Generation.Instructions.AsModality("text").String(); got != want {
			t.Fatalf("stale confirmation instructions = %q, want %q", got, want)
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for stale confirmation prompt")
	}

	select {
	case result := <-task.Result:
		t.Fatalf("task completed with %#v, want no completion for stale confirmation", result)
	default:
	}
}

func TestGetNameTaskDeclineFailsTask(t *testing.T) {
	task := NewGetNameTask(GetNameOptions{FirstName: true, RequireConfirmationSet: true})
	tool := &declineNameCaptureTool{task: task}

	out, err := tool.Execute(context.Background(), `{"reason":"privacy"}`)
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	if out != "" {
		t.Fatalf("Execute() output = %q, want empty output after decline", out)
	}
	_, err = task.WaitAny(context.Background())
	var toolErr llm.ToolError
	if !errors.As(err, &toolErr) {
		t.Fatalf("WaitAny() error = %T %v, want ToolError", err, err)
	}
	want := "couldn't get the name: privacy"
	if toolErr.Message != want {
		t.Fatalf("ToolError.Message = %q, want %q", toolErr.Message, want)
	}
}

func TestDeclineNameCaptureToolUsesRunContextCurrentAgent(t *testing.T) {
	staleTask := NewGetNameTask(GetNameOptions{FirstName: true})
	currentTask := NewGetNameTask(GetNameOptions{FirstName: true})
	session := agent.NewAgentSession(currentTask, nil, agent.AgentSessionOptions{})
	ctx := agent.WithRunContext(context.Background(), agent.NewRunContext(session, nil, nil))
	tool := &declineNameCaptureTool{task: staleTask}

	out, err := tool.Execute(ctx, `{"reason":"privacy current name"}`)
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	if out != "" {
		t.Fatalf("Execute() output = %q, want empty output after decline", out)
	}

	select {
	case err := <-currentTask.Err:
		var toolErr llm.ToolError
		if !errors.As(err, &toolErr) {
			t.Fatalf("current task error = %T %v, want ToolError", err, err)
		}
		want := "couldn't get the name: privacy current name"
		if toolErr.Message != want {
			t.Fatalf("ToolError.Message = %q, want %q", toolErr.Message, want)
		}
	case <-time.After(time.Second):
		t.Fatal("current task did not fail after decline_name_capture")
	}

	select {
	case err := <-staleTask.Err:
		t.Fatalf("stale task failed with %v, want decline routed to current agent", err)
	default:
	}
}

func TestDeclineNameCaptureToolUsesReferenceSchema(t *testing.T) {
	tool := &declineNameCaptureTool{task: NewGetNameTask(GetNameOptions{FirstName: true})}

	wantDescription := "Handles the case when the user explicitly declines to provide their name."
	if got := tool.Description(); got != wantDescription {
		t.Fatalf("decline_name_capture description = %q, want %q", got, wantDescription)
	}
	properties, ok := tool.Parameters()["properties"].(map[string]any)
	if !ok {
		t.Fatalf("properties = %#v, want map", tool.Parameters()["properties"])
	}
	reason, ok := properties["reason"].(map[string]any)
	if !ok {
		t.Fatalf("reason schema = %#v, want map", properties["reason"])
	}
	wantParam := "A short explanation of why the user declined to provide their name"
	if got := reason["description"]; got != wantParam {
		t.Fatalf("reason description = %#v, want %q", got, wantParam)
	}
}

type referenceNameExtraTool struct {
	id string
}

func (t referenceNameExtraTool) ID() string          { return t.id }
func (t referenceNameExtraTool) Name() string        { return t.id }
func (t referenceNameExtraTool) Description() string { return "reference extra name tool" }
func (t referenceNameExtraTool) Parameters() map[string]any {
	return map[string]any{"type": "object", "properties": map[string]any{}}
}
func (t referenceNameExtraTool) Execute(ctx context.Context, args string) (string, error) {
	return "", nil
}
