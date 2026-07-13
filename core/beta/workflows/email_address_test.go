package workflows

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/cavos-io/rtp-agent/core/agent"
	"github.com/cavos-io/rtp-agent/core/beta"
	"github.com/cavos-io/rtp-agent/core/llm"
)

func TestGetEmailTaskRecordsEmailWithoutConfirmation(t *testing.T) {
	task := NewGetEmailTask(GetEmailOptions{RequireConfirmationSet: true})
	tool := &updateEmailTool{task: task}

	out, err := tool.Execute(context.Background(), `{"email":"ada@example.com"}`)
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	// Reference returns None after no-confirm completion, avoiding extra post-completion tool chatter.
	if out != "" {
		t.Fatalf("Execute() output = %q, want empty output after no-confirm completion", out)
	}

	select {
	case result := <-task.Result:
		if result.Email != "ada@example.com" {
			t.Fatalf("Email = %q, want ada@example.com", result.Email)
		}
	default:
		t.Fatal("task did not complete after valid email")
	}
}

func TestGetEmailTaskNormalizesSpokenSymbols(t *testing.T) {
	task := NewGetEmailTask(GetEmailOptions{RequireConfirmationSet: true})
	tool := &updateEmailTool{task: task}

	out, err := tool.Execute(context.Background(), `{"email":"ada at example dot com"}`)
	if err != nil {
		t.Fatalf("Execute() error = %v, want spoken email symbols accepted", err)
	}
	if out != "" {
		t.Fatalf("Execute() output = %q, want empty output after no-confirm completion", out)
	}

	select {
	case result := <-task.Result:
		if result.Email != "ada@example.com" {
			t.Fatalf("Email = %q, want spoken symbols normalized", result.Email)
		}
	default:
		t.Fatal("task did not complete after spoken-symbol email")
	}
}

func TestGetEmailTaskNormalizesGeeMailDomain(t *testing.T) {
	task := NewGetEmailTask(GetEmailOptions{RequireConfirmationSet: true})
	tool := &updateEmailTool{task: task}

	out, err := tool.Execute(context.Background(), `{"email":"john at gee mail dot com"}`)
	if err != nil {
		t.Fatalf("Execute() error = %v, want spoken gee mail domain accepted", err)
	}
	if out != "" {
		t.Fatalf("Execute() output = %q, want empty output after no-confirm completion", out)
	}

	select {
	case result := <-task.Result:
		if result.Email != "john@gmail.com" {
			t.Fatalf("Email = %q, want gee mail normalized to gmail", result.Email)
		}
	default:
		t.Fatal("task did not complete after spoken gee mail email")
	}
}

func TestGetEmailTaskNormalizesGeeMaleDomain(t *testing.T) {
	task := NewGetEmailTask(GetEmailOptions{RequireConfirmationSet: true})
	tool := &updateEmailTool{task: task}

	out, err := tool.Execute(context.Background(), `{"email":"john at gee male dot com"}`)
	if err != nil {
		t.Fatalf("Execute() error = %v, want noisy spoken gee male domain accepted", err)
	}
	if out != "" {
		t.Fatalf("Execute() output = %q, want empty output after no-confirm completion", out)
	}

	select {
	case result := <-task.Result:
		if result.Email != "john@gmail.com" {
			t.Fatalf("Email = %q, want gee male normalized to gmail", result.Email)
		}
	default:
		t.Fatal("task did not complete after noisy spoken gee male email")
	}
}

func TestGetEmailTaskNormalizesYahHooDomain(t *testing.T) {
	task := NewGetEmailTask(GetEmailOptions{RequireConfirmationSet: true})
	tool := &updateEmailTool{task: task}

	out, err := tool.Execute(context.Background(), `{"email":"susan at yah hoo dot com"}`)
	if err != nil {
		t.Fatalf("Execute() error = %v, want spoken yah hoo domain accepted", err)
	}
	if out != "" {
		t.Fatalf("Execute() output = %q, want empty output after no-confirm completion", out)
	}

	select {
	case result := <-task.Result:
		if result.Email != "susan@yahoo.com" {
			t.Fatalf("Email = %q, want yah hoo normalized to yahoo", result.Email)
		}
	default:
		t.Fatal("task did not complete after spoken yah hoo email")
	}
}

func TestGetEmailTaskNormalizesYahWhoDomain(t *testing.T) {
	task := NewGetEmailTask(GetEmailOptions{RequireConfirmationSet: true})
	tool := &updateEmailTool{task: task}

	out, err := tool.Execute(context.Background(), `{"email":"susan at yah who dot com"}`)
	if err != nil {
		t.Fatalf("Execute() error = %v, want noisy spoken yah who domain accepted", err)
	}
	if out != "" {
		t.Fatalf("Execute() output = %q, want empty output after no-confirm completion", out)
	}

	select {
	case result := <-task.Result:
		if result.Email != "susan@yahoo.com" {
			t.Fatalf("Email = %q, want yah who normalized to yahoo", result.Email)
		}
	default:
		t.Fatal("task did not complete after noisy spoken yah who email")
	}
}

func TestGetEmailTaskNormalizesEyeCloudDomain(t *testing.T) {
	task := NewGetEmailTask(GetEmailOptions{RequireConfirmationSet: true})
	tool := &updateEmailTool{task: task}

	out, err := tool.Execute(context.Background(), `{"email":"sam at eye cloud dot com"}`)
	if err != nil {
		t.Fatalf("Execute() error = %v, want spoken eye cloud domain accepted", err)
	}
	if out != "" {
		t.Fatalf("Execute() output = %q, want empty output after no-confirm completion", out)
	}

	select {
	case result := <-task.Result:
		if result.Email != "sam@icloud.com" {
			t.Fatalf("Email = %q, want eye cloud normalized to icloud", result.Email)
		}
	default:
		t.Fatal("task did not complete after spoken eye cloud email")
	}
}

func TestGetEmailTaskNormalizesAyOhEllDomain(t *testing.T) {
	task := NewGetEmailTask(GetEmailOptions{RequireConfirmationSet: true})
	tool := &updateEmailTool{task: task}

	out, err := tool.Execute(context.Background(), `{"email":"sam at ay oh ell dot com"}`)
	if err != nil {
		t.Fatalf("Execute() error = %v, want spoken ay oh ell domain accepted", err)
	}
	if out != "" {
		t.Fatalf("Execute() output = %q, want empty output after no-confirm completion", out)
	}

	select {
	case result := <-task.Result:
		if result.Email != "sam@aol.com" {
			t.Fatalf("Email = %q, want ay oh ell normalized to aol", result.Email)
		}
	default:
		t.Fatal("task did not complete after spoken ay oh ell email")
	}
}

func TestGetEmailTaskNormalizesAyOweEllDomain(t *testing.T) {
	task := NewGetEmailTask(GetEmailOptions{RequireConfirmationSet: true})
	tool := &updateEmailTool{task: task}

	out, err := tool.Execute(context.Background(), `{"email":"sam at ay owe ell dot com"}`)
	if err != nil {
		t.Fatalf("Execute() error = %v, want spoken ay owe ell domain accepted", err)
	}
	if out != "" {
		t.Fatalf("Execute() output = %q, want empty output after no-confirm completion", out)
	}

	select {
	case result := <-task.Result:
		if result.Email != "sam@aol.com" {
			t.Fatalf("Email = %q, want ay owe ell normalized to aol", result.Email)
		}
	default:
		t.Fatal("task did not complete after spoken ay owe ell email")
	}
}

func TestGetEmailTaskNormalizesAyOEllDomain(t *testing.T) {
	task := NewGetEmailTask(GetEmailOptions{RequireConfirmationSet: true})
	tool := &updateEmailTool{task: task}

	out, err := tool.Execute(context.Background(), `{"email":"sam at ay o ell dot com"}`)
	if err != nil {
		t.Fatalf("Execute() error = %v, want spoken ay o ell domain accepted", err)
	}
	if out != "" {
		t.Fatalf("Execute() output = %q, want empty output after no-confirm completion", out)
	}

	select {
	case result := <-task.Result:
		if result.Email != "sam@aol.com" {
			t.Fatalf("Email = %q, want ay o ell normalized to aol", result.Email)
		}
	default:
		t.Fatal("task did not complete after spoken ay o ell email")
	}
}

func TestGetEmailTaskNormalizesAyeOEllDomain(t *testing.T) {
	task := NewGetEmailTask(GetEmailOptions{RequireConfirmationSet: true})
	tool := &updateEmailTool{task: task}

	out, err := tool.Execute(context.Background(), `{"email":"sam at aye o ell dot com"}`)
	if err != nil {
		t.Fatalf("Execute() error = %v, want spoken aye o ell domain accepted", err)
	}
	if out != "" {
		t.Fatalf("Execute() output = %q, want empty output after no-confirm completion", out)
	}

	select {
	case result := <-task.Result:
		if result.Email != "sam@aol.com" {
			t.Fatalf("Email = %q, want aye o ell normalized to aol", result.Email)
		}
	default:
		t.Fatal("task did not complete after spoken aye o ell email")
	}
}

func TestGetEmailTaskNormalizesEmEssEnDomain(t *testing.T) {
	task := NewGetEmailTask(GetEmailOptions{RequireConfirmationSet: true})
	tool := &updateEmailTool{task: task}

	out, err := tool.Execute(context.Background(), `{"email":"sam at em ess en dot com"}`)
	if err != nil {
		t.Fatalf("Execute() error = %v, want spoken em ess en domain accepted", err)
	}
	if out != "" {
		t.Fatalf("Execute() output = %q, want empty output after no-confirm completion", out)
	}

	select {
	case result := <-task.Result:
		if result.Email != "sam@msn.com" {
			t.Fatalf("Email = %q, want em ess en normalized to msn", result.Email)
		}
	default:
		t.Fatal("task did not complete after spoken em ess en email")
	}
}

func TestGetEmailTaskNormalizesAyTeeTeeDomain(t *testing.T) {
	task := NewGetEmailTask(GetEmailOptions{RequireConfirmationSet: true})
	tool := &updateEmailTool{task: task}

	out, err := tool.Execute(context.Background(), `{"email":"sam at ay tee tee dot net"}`)
	if err != nil {
		t.Fatalf("Execute() error = %v, want spoken ay tee tee domain accepted", err)
	}
	if out != "" {
		t.Fatalf("Execute() output = %q, want empty output after no-confirm completion", out)
	}

	select {
	case result := <-task.Result:
		if result.Email != "sam@att.net" {
			t.Fatalf("Email = %q, want ay tee tee normalized to att", result.Email)
		}
	default:
		t.Fatal("task did not complete after spoken ay tee tee email")
	}
}

func TestGetEmailTaskNormalizesAyeTeeTeeDomain(t *testing.T) {
	task := NewGetEmailTask(GetEmailOptions{RequireConfirmationSet: true})
	tool := &updateEmailTool{task: task}

	out, err := tool.Execute(context.Background(), `{"email":"sam at aye tee tee dot net"}`)
	if err != nil {
		t.Fatalf("Execute() error = %v, want spoken aye tee tee domain accepted", err)
	}
	if out != "" {
		t.Fatalf("Execute() output = %q, want empty output after no-confirm completion", out)
	}

	select {
	case result := <-task.Result:
		if result.Email != "sam@att.net" {
			t.Fatalf("Email = %q, want aye tee tee normalized to att", result.Email)
		}
	default:
		t.Fatal("task did not complete after spoken aye tee tee email")
	}
}

func TestGetEmailTaskNormalizesEssBeeSeeGlobalDomain(t *testing.T) {
	task := NewGetEmailTask(GetEmailOptions{RequireConfirmationSet: true})
	tool := &updateEmailTool{task: task}

	out, err := tool.Execute(context.Background(), `{"email":"sam at ess bee see global dot net"}`)
	if err != nil {
		t.Fatalf("Execute() error = %v, want spoken ess bee see global domain accepted", err)
	}
	if out != "" {
		t.Fatalf("Execute() output = %q, want empty output after no-confirm completion", out)
	}

	select {
	case result := <-task.Result:
		if result.Email != "sam@sbcglobal.net" {
			t.Fatalf("Email = %q, want ess bee see global normalized to sbcglobal", result.Email)
		}
	default:
		t.Fatal("task did not complete after spoken ess bee see global email")
	}
}

func TestGetEmailTaskNormalizesSeeOhExDomain(t *testing.T) {
	task := NewGetEmailTask(GetEmailOptions{RequireConfirmationSet: true})
	tool := &updateEmailTool{task: task}

	out, err := tool.Execute(context.Background(), `{"email":"sam at see oh ex dot net"}`)
	if err != nil {
		t.Fatalf("Execute() error = %v, want spoken see oh ex domain accepted", err)
	}
	if out != "" {
		t.Fatalf("Execute() output = %q, want empty output after no-confirm completion", out)
	}

	select {
	case result := <-task.Result:
		if result.Email != "sam@cox.net" {
			t.Fatalf("Email = %q, want see oh ex normalized to cox", result.Email)
		}
	default:
		t.Fatal("task did not complete after spoken see oh ex email")
	}
}

func TestGetEmailTaskNormalizesSeeOweExDomain(t *testing.T) {
	task := NewGetEmailTask(GetEmailOptions{RequireConfirmationSet: true})
	tool := &updateEmailTool{task: task}

	out, err := tool.Execute(context.Background(), `{"email":"sam at see owe ex dot net"}`)
	if err != nil {
		t.Fatalf("Execute() error = %v, want spoken see owe ex domain accepted", err)
	}
	if out != "" {
		t.Fatalf("Execute() output = %q, want empty output after no-confirm completion", out)
	}

	select {
	case result := <-task.Result:
		if result.Email != "sam@cox.net" {
			t.Fatalf("Email = %q, want see owe ex normalized to cox", result.Email)
		}
	default:
		t.Fatal("task did not complete after spoken see owe ex email")
	}
}

func TestGetEmailTaskNormalizesPunctuatedSpokenSymbols(t *testing.T) {
	task := NewGetEmailTask(GetEmailOptions{RequireConfirmationSet: true})
	tool := &updateEmailTool{task: task}

	_, err := tool.Execute(context.Background(), `{"email":"ada at, example dot. com"}`)
	if err != nil {
		t.Fatalf("Execute() error = %v, want punctuated spoken email symbols accepted", err)
	}

	select {
	case result := <-task.Result:
		if result.Email != "ada@example.com" {
			t.Fatalf("Email = %q, want punctuated spoken symbols normalized", result.Email)
		}
	default:
		t.Fatal("task did not complete after punctuated spoken-symbol email")
	}
}

func TestGetEmailTaskNormalizesSpokenAtSign(t *testing.T) {
	task := NewGetEmailTask(GetEmailOptions{RequireConfirmationSet: true})
	tool := &updateEmailTool{task: task}

	_, err := tool.Execute(context.Background(), `{"email":"ada at sign example dot com"}`)
	if err != nil {
		t.Fatalf("Execute() error = %v, want spoken at sign accepted", err)
	}

	select {
	case result := <-task.Result:
		if result.Email != "ada@example.com" {
			t.Fatalf("Email = %q, want spoken at sign normalized", result.Email)
		}
	default:
		t.Fatal("task did not complete after spoken at sign email")
	}
}

func TestGetEmailTaskNormalizesSpokenAtSymbol(t *testing.T) {
	task := NewGetEmailTask(GetEmailOptions{RequireConfirmationSet: true})
	tool := &updateEmailTool{task: task}

	_, err := tool.Execute(context.Background(), `{"email":"ada at symbol example dot com"}`)
	if err != nil {
		t.Fatalf("Execute() error = %v, want spoken at symbol accepted", err)
	}

	select {
	case result := <-task.Result:
		if result.Email != "ada@example.com" {
			t.Fatalf("Email = %q, want spoken at symbol normalized", result.Email)
		}
	default:
		t.Fatal("task did not complete after spoken at symbol email")
	}
}

func TestGetEmailTaskNormalizesSpokenAtTheRate(t *testing.T) {
	task := NewGetEmailTask(GetEmailOptions{RequireConfirmationSet: true})
	tool := &updateEmailTool{task: task}

	_, err := tool.Execute(context.Background(), `{"email":"ada at the rate example dot com"}`)
	if err != nil {
		t.Fatalf("Execute() error = %v, want spoken at-the-rate accepted", err)
	}

	select {
	case result := <-task.Result:
		if result.Email != "ada@example.com" {
			t.Fatalf("Email = %q, want spoken at-the-rate normalized", result.Email)
		}
	default:
		t.Fatal("task did not complete after spoken at-the-rate email")
	}
}

func TestGetEmailTaskNormalizesSpokenAtRate(t *testing.T) {
	task := NewGetEmailTask(GetEmailOptions{RequireConfirmationSet: true})
	tool := &updateEmailTool{task: task}

	_, err := tool.Execute(context.Background(), `{"email":"ada at rate example dot com"}`)
	if err != nil {
		t.Fatalf("Execute() error = %v, want spoken at-rate accepted", err)
	}

	select {
	case result := <-task.Result:
		if result.Email != "ada@example.com" {
			t.Fatalf("Email = %q, want spoken at-rate normalized", result.Email)
		}
	default:
		t.Fatal("task did not complete after spoken at-rate email")
	}
}

func TestGetEmailTaskNormalizesSpokenSingleDigit(t *testing.T) {
	task := NewGetEmailTask(GetEmailOptions{RequireConfirmationSet: true})
	tool := &updateEmailTool{task: task}

	_, err := tool.Execute(context.Background(), `{"email":"alex single five at example dot com"}`)
	if err != nil {
		t.Fatalf("Execute() error = %v, want single spoken digit accepted", err)
	}

	select {
	case result := <-task.Result:
		if result.Email != "alex5@example.com" {
			t.Fatalf("Email = %q, want single spoken digit normalized", result.Email)
		}
	default:
		t.Fatal("task did not complete after single spoken digit email")
	}
}

func TestGetEmailTaskNormalizesSpokenDotSymbol(t *testing.T) {
	task := NewGetEmailTask(GetEmailOptions{RequireConfirmationSet: true})
	tool := &updateEmailTool{task: task}

	_, err := tool.Execute(context.Background(), `{"email":"ada at example dot symbol com"}`)
	if err != nil {
		t.Fatalf("Execute() error = %v, want spoken dot symbol accepted", err)
	}

	select {
	case result := <-task.Result:
		if result.Email != "ada@example.com" {
			t.Fatalf("Email = %q, want spoken dot symbol normalized", result.Email)
		}
	default:
		t.Fatal("task did not complete after spoken dot symbol email")
	}
}

func TestGetEmailTaskNormalizesSpokenDotSign(t *testing.T) {
	task := NewGetEmailTask(GetEmailOptions{RequireConfirmationSet: true})
	tool := &updateEmailTool{task: task}

	_, err := tool.Execute(context.Background(), `{"email":"ada at example dot sign com"}`)
	if err != nil {
		t.Fatalf("Execute() error = %v, want spoken dot sign accepted", err)
	}

	select {
	case result := <-task.Result:
		if result.Email != "ada@example.com" {
			t.Fatalf("Email = %q, want spoken dot sign normalized", result.Email)
		}
	default:
		t.Fatal("task did not complete after spoken dot sign email")
	}
}

func TestGetEmailTaskNormalizesSpokenFullStop(t *testing.T) {
	task := NewGetEmailTask(GetEmailOptions{RequireConfirmationSet: true})
	tool := &updateEmailTool{task: task}

	_, err := tool.Execute(context.Background(), `{"email":"ada at example full stop com"}`)
	if err != nil {
		t.Fatalf("Execute() error = %v, want spoken full stop accepted", err)
	}

	select {
	case result := <-task.Result:
		if result.Email != "ada@example.com" {
			t.Fatalf("Email = %q, want spoken full stop normalized", result.Email)
		}
	default:
		t.Fatal("task did not complete after spoken full stop email")
	}
}

func TestGetEmailTaskNormalizesSpokenPoint(t *testing.T) {
	task := NewGetEmailTask(GetEmailOptions{RequireConfirmationSet: true})
	tool := &updateEmailTool{task: task}

	_, err := tool.Execute(context.Background(), `{"email":"ada at example point com"}`)
	if err != nil {
		t.Fatalf("Execute() error = %v, want spoken point accepted", err)
	}

	select {
	case result := <-task.Result:
		if result.Email != "ada@example.com" {
			t.Fatalf("Email = %q, want spoken point normalized", result.Email)
		}
	default:
		t.Fatal("task did not complete after spoken point email")
	}
}

func TestGetEmailTaskNormalizesSpokenDotCalm(t *testing.T) {
	task := NewGetEmailTask(GetEmailOptions{RequireConfirmationSet: true})
	tool := &updateEmailTool{task: task}

	_, err := tool.Execute(context.Background(), `{"email":"ada at example dot calm"}`)
	if err != nil {
		t.Fatalf("Execute() error = %v, want noisy spoken dot calm accepted", err)
	}

	select {
	case result := <-task.Result:
		if result.Email != "ada@example.com" {
			t.Fatalf("Email = %q, want noisy dot calm normalized to dot com", result.Email)
		}
	default:
		t.Fatal("task did not complete after noisy spoken dot calm email")
	}
}

func TestGetEmailTaskNormalizesSpokenDotCon(t *testing.T) {
	task := NewGetEmailTask(GetEmailOptions{RequireConfirmationSet: true})
	tool := &updateEmailTool{task: task}

	_, err := tool.Execute(context.Background(), `{"email":"ada at example dot con"}`)
	if err != nil {
		t.Fatalf("Execute() error = %v, want noisy spoken dot con accepted", err)
	}

	select {
	case result := <-task.Result:
		if result.Email != "ada@example.com" {
			t.Fatalf("Email = %q, want noisy dot con normalized to dot com", result.Email)
		}
	default:
		t.Fatal("task did not complete after noisy spoken dot con email")
	}
}

func TestGetEmailTaskNormalizesFusedSpokenDotCom(t *testing.T) {
	task := NewGetEmailTask(GetEmailOptions{RequireConfirmationSet: true})
	tool := &updateEmailTool{task: task}

	_, err := tool.Execute(context.Background(), `{"email":"ada at example dotcom"}`)
	if err != nil {
		t.Fatalf("Execute() error = %v, want fused spoken dotcom accepted", err)
	}

	select {
	case result := <-task.Result:
		if result.Email != "ada@example.com" {
			t.Fatalf("Email = %q, want fused dotcom normalized", result.Email)
		}
	default:
		t.Fatal("task did not complete after fused dotcom email")
	}
}

func TestGetEmailTaskNormalizesFusedSpokenDotUK(t *testing.T) {
	task := NewGetEmailTask(GetEmailOptions{RequireConfirmationSet: true})
	tool := &updateEmailTool{task: task}

	_, err := tool.Execute(context.Background(), `{"email":"susan under score smith at yahoo dotco dotuk"}`)
	if err != nil {
		t.Fatalf("Execute() error = %v, want fused spoken dotuk accepted", err)
	}

	select {
	case result := <-task.Result:
		if result.Email != "susan_smith@yahoo.co.uk" {
			t.Fatalf("Email = %q, want fused dotuk normalized", result.Email)
		}
	default:
		t.Fatal("task did not complete after fused dotuk email")
	}
}

func TestGetEmailTaskNormalizesFusedSpokenDotCA(t *testing.T) {
	task := NewGetEmailTask(GetEmailOptions{RequireConfirmationSet: true})
	tool := &updateEmailTool{task: task}

	_, err := tool.Execute(context.Background(), `{"email":"ada at example dotca"}`)
	if err != nil {
		t.Fatalf("Execute() error = %v, want fused spoken dotca accepted", err)
	}

	select {
	case result := <-task.Result:
		if result.Email != "ada@example.ca" {
			t.Fatalf("Email = %q, want fused dotca normalized", result.Email)
		}
	default:
		t.Fatal("task did not complete after fused dotca email")
	}
}

func TestGetEmailTaskNormalizesSplitUnderscore(t *testing.T) {
	task := NewGetEmailTask(GetEmailOptions{RequireConfirmationSet: true})
	tool := &updateEmailTool{task: task}

	_, err := tool.Execute(context.Background(), `{"email":"susan under score smith at example dot com"}`)
	if err != nil {
		t.Fatalf("Execute() error = %v, want split underscore accepted", err)
	}

	select {
	case result := <-task.Result:
		if result.Email != "susan_smith@example.com" {
			t.Fatalf("Email = %q, want split underscore normalized", result.Email)
		}
	default:
		t.Fatal("task did not complete after split underscore email")
	}
}

func TestGetEmailTaskNormalizesSpokenUnderscoreSign(t *testing.T) {
	task := NewGetEmailTask(GetEmailOptions{RequireConfirmationSet: true})
	tool := &updateEmailTool{task: task}

	_, err := tool.Execute(context.Background(), `{"email":"susan underscore sign smith at example dot com"}`)
	if err != nil {
		t.Fatalf("Execute() error = %v, want spoken underscore sign accepted", err)
	}

	select {
	case result := <-task.Result:
		if result.Email != "susan_smith@example.com" {
			t.Fatalf("Email = %q, want spoken underscore sign normalized", result.Email)
		}
	default:
		t.Fatal("task did not complete after spoken underscore sign email")
	}
}

func TestGetEmailTaskNormalizesSplitHyphen(t *testing.T) {
	task := NewGetEmailTask(GetEmailOptions{RequireConfirmationSet: true})
	tool := &updateEmailTool{task: task}

	_, err := tool.Execute(context.Background(), `{"email":"dave hy phen b at example dot com"}`)
	if err != nil {
		t.Fatalf("Execute() error = %v, want split hyphen accepted", err)
	}

	select {
	case result := <-task.Result:
		if result.Email != "dave-b@example.com" {
			t.Fatalf("Email = %q, want split hyphen normalized", result.Email)
		}
	default:
		t.Fatal("task did not complete after split hyphen email")
	}
}

func TestGetEmailTaskNormalizesSpokenMinus(t *testing.T) {
	task := NewGetEmailTask(GetEmailOptions{RequireConfirmationSet: true})
	tool := &updateEmailTool{task: task}

	_, err := tool.Execute(context.Background(), `{"email":"dave minus b at example dot com"}`)
	if err != nil {
		t.Fatalf("Execute() error = %v, want spoken minus accepted as hyphen", err)
	}

	select {
	case result := <-task.Result:
		if result.Email != "dave-b@example.com" {
			t.Fatalf("Email = %q, want spoken minus normalized", result.Email)
		}
	default:
		t.Fatal("task did not complete after spoken minus email")
	}
}

func TestGetEmailTaskNormalizesSpokenMinusSign(t *testing.T) {
	task := NewGetEmailTask(GetEmailOptions{RequireConfirmationSet: true})
	tool := &updateEmailTool{task: task}

	_, err := tool.Execute(context.Background(), `{"email":"dave minus sign b at example dot com"}`)
	if err != nil {
		t.Fatalf("Execute() error = %v, want spoken minus sign accepted as hyphen", err)
	}

	select {
	case result := <-task.Result:
		if result.Email != "dave-b@example.com" {
			t.Fatalf("Email = %q, want spoken minus sign normalized", result.Email)
		}
	default:
		t.Fatal("task did not complete after spoken minus sign email")
	}
}

func TestGetEmailTaskNormalizesSpokenPlusSign(t *testing.T) {
	task := NewGetEmailTask(GetEmailOptions{RequireConfirmationSet: true})
	tool := &updateEmailTool{task: task}

	_, err := tool.Execute(context.Background(), `{"email":"jane plus sign alerts at example dot com"}`)
	if err != nil {
		t.Fatalf("Execute() error = %v, want spoken plus sign accepted", err)
	}

	select {
	case result := <-task.Result:
		if result.Email != "jane+alerts@example.com" {
			t.Fatalf("Email = %q, want spoken plus sign normalized", result.Email)
		}
	default:
		t.Fatal("task did not complete after spoken plus sign email")
	}
}

func TestGetEmailTaskNormalizesSpokenPlusSymbol(t *testing.T) {
	task := NewGetEmailTask(GetEmailOptions{RequireConfirmationSet: true})
	tool := &updateEmailTool{task: task}

	_, err := tool.Execute(context.Background(), `{"email":"jane plus symbol alerts at example dot com"}`)
	if err != nil {
		t.Fatalf("Execute() error = %v, want spoken plus symbol accepted", err)
	}

	select {
	case result := <-task.Result:
		if result.Email != "jane+alerts@example.com" {
			t.Fatalf("Email = %q, want spoken plus symbol normalized", result.Email)
		}
	default:
		t.Fatal("task did not complete after spoken plus symbol email")
	}
}

func TestGetEmailTaskNormalizesSpokenPlusKey(t *testing.T) {
	task := NewGetEmailTask(GetEmailOptions{RequireConfirmationSet: true})
	tool := &updateEmailTool{task: task}

	_, err := tool.Execute(context.Background(), `{"email":"jane plus key alerts at example dot com"}`)
	if err != nil {
		t.Fatalf("Execute() error = %v, want spoken plus key accepted", err)
	}

	select {
	case result := <-task.Result:
		if result.Email != "jane+alerts@example.com" {
			t.Fatalf("Email = %q, want spoken plus key normalized", result.Email)
		}
	default:
		t.Fatal("task did not complete after spoken plus key email")
	}
}

func TestGetEmailTaskNormalizesSpokenMarkSuffixes(t *testing.T) {
	task := NewGetEmailTask(GetEmailOptions{RequireConfirmationSet: true})
	tool := &updateEmailTool{task: task}

	_, err := tool.Execute(context.Background(), `{"email":"jane plus mark alerts at example dot mark com"}`)
	if err != nil {
		t.Fatalf("Execute() error = %v, want spoken mark suffixes accepted", err)
	}

	select {
	case result := <-task.Result:
		if result.Email != "jane+alerts@example.com" {
			t.Fatalf("Email = %q, want spoken mark suffixes normalized", result.Email)
		}
	default:
		t.Fatal("task did not complete after spoken mark suffix email")
	}
}

func TestGetEmailTaskNormalizesSpokenSpelling(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  string
	}{
		{
			name:  "name followed by spelling",
			input: "theo t h e o at livekit dot io",
			want:  "theo@livekit.io",
		},
		{
			name:  "letter-name spelling local part",
			input: "theo tee h e o at livekit dot io",
			want:  "theo@livekit.io",
		},
		{
			name:  "oh and owe spelling aliases local part",
			input: "theo tee h e oh at livekit dot eye owe",
			want:  "theo@livekit.io",
		},
		{
			name:  "spelled preamble",
			input: "my email is spelled a d a at example dot com",
			want:  "ada@example.com",
		},
		{
			name:  "obvious spoken digits",
			input: "mike b two two at example dot com",
			want:  "mikeb22@example.com",
		},
		{
			name:  "letter name before spoken digits",
			input: "mike bee two two at example dot com",
			want:  "mikeb22@example.com",
		},
		{
			name:  "letter homophone before spoken digits",
			input: "mike be two two at example dot com",
			want:  "mikeb22@example.com",
		},
		{
			name:  "c letter name before spoken digits",
			input: "casey see two at example dot com",
			want:  "caseyc2@example.com",
		},
		{
			name:  "c spelled name before spoken digits",
			input: "casey cee two at example dot com",
			want:  "caseyc2@example.com",
		},
		{
			name:  "c homophone before spoken digits",
			input: "casey sea two at example dot com",
			want:  "caseyc2@example.com",
		},
		{
			name:  "common letter names before spoken digits",
			input: "alpha ay two at example dot com",
			want:  "alphaa2@example.com",
		},
		{
			name:  "common consonant names before spoken digits",
			input: "delta dee three at example dot com",
			want:  "deltad3@example.com",
		},
		{
			name:  "common later letter names before spoken digits",
			input: "zara zed nine at example dot com",
			want:  "zaraz9@example.com",
		},
		{
			name:  "spoken letter names in tld",
			input: "ada at example dot see oh em",
			want:  "ada@example.com",
		},
		{
			name:  "spoken e letter name in tld",
			input: "sam at example dot en ee tee",
			want:  "sam@example.net",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			task := NewGetEmailTask(GetEmailOptions{RequireConfirmationSet: true})
			tool := &updateEmailTool{task: task}

			out, err := tool.Execute(context.Background(), `{"email":"`+tt.input+`"}`)
			if err != nil {
				t.Fatalf("Execute() error = %v, want spoken spelling accepted", err)
			}
			if out != "" {
				t.Fatalf("Execute() output = %q, want empty output after no-confirm completion", out)
			}

			select {
			case result := <-task.Result:
				if result.Email != tt.want {
					t.Fatalf("Email = %q, want spoken spelling normalized to %q", result.Email, tt.want)
				}
			default:
				t.Fatal("task did not complete after spoken-spelling email")
			}
		})
	}
}

func TestGetEmailTaskNormalizesLetterNameBeforeNaughtDigit(t *testing.T) {
	task := NewGetEmailTask(GetEmailOptions{RequireConfirmationSet: true})
	tool := &updateEmailTool{task: task}

	out, err := tool.Execute(context.Background(), `{"email":"mike bee naught at example dot com"}`)
	if err != nil {
		t.Fatalf("Execute() error = %v, want letter name before naught digit accepted", err)
	}
	if out != "" {
		t.Fatalf("Execute() output = %q, want empty output after no-confirm completion", out)
	}

	select {
	case result := <-task.Result:
		if result.Email != "mikeb0@example.com" {
			t.Fatalf("Email = %q, want letter name before naught digit normalized", result.Email)
		}
	default:
		t.Fatal("task did not complete after letter-name naught email")
	}
}

func TestGetEmailTaskNormalizesNoisySTTDigitHomophones(t *testing.T) {
	task := NewGetEmailTask(GetEmailOptions{RequireConfirmationSet: true})
	tool := &updateEmailTool{task: task}

	out, err := tool.Execute(context.Background(), `{"email":"alex for to ate at example dot com"}`)
	if err != nil {
		t.Fatalf("Execute() error = %v, want noisy STT digit homophones accepted", err)
	}
	if out != "" {
		t.Fatalf("Execute() output = %q, want empty output after no-confirm completion", out)
	}

	select {
	case result := <-task.Result:
		if result.Email != "alex428@example.com" {
			t.Fatalf("Email = %q, want noisy STT digit homophones normalized", result.Email)
		}
	default:
		t.Fatal("task did not complete after noisy STT email")
	}
}

func TestGetEmailTaskNormalizesForeHomophone(t *testing.T) {
	task := NewGetEmailTask(GetEmailOptions{RequireConfirmationSet: true})
	tool := &updateEmailTool{task: task}

	out, err := tool.Execute(context.Background(), `{"email":"alex fore at example dot com"}`)
	if err != nil {
		t.Fatalf("Execute() error = %v, want fore homophone accepted", err)
	}
	if out != "" {
		t.Fatalf("Execute() output = %q, want empty output after no-confirm completion", out)
	}

	select {
	case result := <-task.Result:
		if result.Email != "alex4@example.com" {
			t.Fatalf("Email = %q, want fore homophone normalized", result.Email)
		}
	default:
		t.Fatal("task did not complete after fore-homophone email")
	}
}

func TestGetEmailTaskNormalizesThreeHomophones(t *testing.T) {
	task := NewGetEmailTask(GetEmailOptions{RequireConfirmationSet: true})
	tool := &updateEmailTool{task: task}

	out, err := tool.Execute(context.Background(), `{"email":"alex tree free at example dot com"}`)
	if err != nil {
		t.Fatalf("Execute() error = %v, want three homophones accepted", err)
	}
	if out != "" {
		t.Fatalf("Execute() output = %q, want empty output after no-confirm completion", out)
	}

	select {
	case result := <-task.Result:
		if result.Email != "alex33@example.com" {
			t.Fatalf("Email = %q, want three homophones normalized", result.Email)
		}
	default:
		t.Fatal("task did not complete after three-homophone email")
	}
}

func TestGetEmailTaskNormalizesSixHomophone(t *testing.T) {
	task := NewGetEmailTask(GetEmailOptions{RequireConfirmationSet: true})
	tool := &updateEmailTool{task: task}

	out, err := tool.Execute(context.Background(), `{"email":"mike b sex at example dot com"}`)
	if err != nil {
		t.Fatalf("Execute() error = %v, want six homophone accepted", err)
	}
	if out != "" {
		t.Fatalf("Execute() output = %q, want empty output after no-confirm completion", out)
	}

	select {
	case result := <-task.Result:
		if result.Email != "mikeb6@example.com" {
			t.Fatalf("Email = %q, want six homophone normalized", result.Email)
		}
	default:
		t.Fatal("task did not complete after six-homophone email")
	}
}

func TestGetEmailTaskNormalizesNinerDigit(t *testing.T) {
	task := NewGetEmailTask(GetEmailOptions{RequireConfirmationSet: true})
	tool := &updateEmailTool{task: task}

	out, err := tool.Execute(context.Background(), `{"email":"alex niner at example dot com"}`)
	if err != nil {
		t.Fatalf("Execute() error = %v, want niner spoken digit accepted", err)
	}
	if out != "" {
		t.Fatalf("Execute() output = %q, want empty output after no-confirm completion", out)
	}

	select {
	case result := <-task.Result:
		if result.Email != "alex9@example.com" {
			t.Fatalf("Email = %q, want niner normalized to 9", result.Email)
		}
	default:
		t.Fatal("task did not complete after niner email")
	}
}

func TestGetEmailTaskNormalizesAughtDigit(t *testing.T) {
	task := NewGetEmailTask(GetEmailOptions{RequireConfirmationSet: true})
	tool := &updateEmailTool{task: task}

	out, err := tool.Execute(context.Background(), `{"email":"alex aught at example dot com"}`)
	if err != nil {
		t.Fatalf("Execute() error = %v, want aught spoken digit accepted", err)
	}
	if out != "" {
		t.Fatalf("Execute() output = %q, want empty output after no-confirm completion", out)
	}

	select {
	case result := <-task.Result:
		if result.Email != "alex0@example.com" {
			t.Fatalf("Email = %q, want aught normalized to 0", result.Email)
		}
	default:
		t.Fatal("task did not complete after aught email")
	}
}

func TestGetEmailTaskNormalizesNaughtDigit(t *testing.T) {
	task := NewGetEmailTask(GetEmailOptions{RequireConfirmationSet: true})
	tool := &updateEmailTool{task: task}

	out, err := tool.Execute(context.Background(), `{"email":"alex naught at example dot com"}`)
	if err != nil {
		t.Fatalf("Execute() error = %v, want naught spoken digit accepted", err)
	}
	if out != "" {
		t.Fatalf("Execute() output = %q, want empty output after no-confirm completion", out)
	}

	select {
	case result := <-task.Result:
		if result.Email != "alex0@example.com" {
			t.Fatalf("Email = %q, want naught normalized to 0", result.Email)
		}
	default:
		t.Fatal("task did not complete after naught email")
	}
}

func TestGetEmailTaskNormalizesNoughtDigit(t *testing.T) {
	task := NewGetEmailTask(GetEmailOptions{RequireConfirmationSet: true})
	tool := &updateEmailTool{task: task}

	out, err := tool.Execute(context.Background(), `{"email":"alex nought at example dot com"}`)
	if err != nil {
		t.Fatalf("Execute() error = %v, want nought spoken digit accepted", err)
	}
	if out != "" {
		t.Fatalf("Execute() output = %q, want empty output after no-confirm completion", out)
	}

	select {
	case result := <-task.Result:
		if result.Email != "alex0@example.com" {
			t.Fatalf("Email = %q, want nought normalized to 0", result.Email)
		}
	default:
		t.Fatal("task did not complete after nought email")
	}
}

func TestGetEmailTaskNormalizesOughtDigit(t *testing.T) {
	task := NewGetEmailTask(GetEmailOptions{RequireConfirmationSet: true})
	tool := &updateEmailTool{task: task}

	out, err := tool.Execute(context.Background(), `{"email":"alex ought at example dot com"}`)
	if err != nil {
		t.Fatalf("Execute() error = %v, want ought spoken digit accepted", err)
	}
	if out != "" {
		t.Fatalf("Execute() output = %q, want empty output after no-confirm completion", out)
	}

	select {
	case result := <-task.Result:
		if result.Email != "alex0@example.com" {
			t.Fatalf("Email = %q, want ought normalized to 0", result.Email)
		}
	default:
		t.Fatal("task did not complete after ought email")
	}
}

func TestGetEmailTaskNormalizesOweDigit(t *testing.T) {
	task := NewGetEmailTask(GetEmailOptions{RequireConfirmationSet: true})
	tool := &updateEmailTool{task: task}

	out, err := tool.Execute(context.Background(), `{"email":"alex owe at example dot com"}`)
	if err != nil {
		t.Fatalf("Execute() error = %v, want owe spoken digit accepted", err)
	}
	if out != "" {
		t.Fatalf("Execute() output = %q, want empty output after no-confirm completion", out)
	}

	select {
	case result := <-task.Result:
		if result.Email != "alex0@example.com" {
			t.Fatalf("Email = %q, want owe normalized to 0", result.Email)
		}
	default:
		t.Fatal("task did not complete after owe email")
	}
}

func TestGetEmailTaskNormalizesTwentyOhSpokenDigits(t *testing.T) {
	tests := []struct {
		input string
		want  string
	}{
		{input: "alex twenty oh five at example dot com", want: "alex2005@example.com"},
		{input: "alex thirty oh seven at example dot com", want: "alex3007@example.com"},
	}

	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			task := NewGetEmailTask(GetEmailOptions{RequireConfirmationSet: true})
			tool := &updateEmailTool{task: task}

			out, err := tool.Execute(context.Background(), `{"email":"`+tt.input+`"}`)
			if err != nil {
				t.Fatalf("Execute() error = %v, want tens-oh spoken digits accepted", err)
			}
			if out != "" {
				t.Fatalf("Execute() output = %q, want empty output after no-confirm completion", out)
			}

			select {
			case result := <-task.Result:
				if result.Email != tt.want {
					t.Fatalf("Email = %q, want tens-oh spoken digits normalized to %q", result.Email, tt.want)
				}
			default:
				t.Fatal("task did not complete after tens-oh spoken email")
			}
		})
	}
}

func TestGetEmailTaskNormalizesSpokenHundredSingleDigitGroup(t *testing.T) {
	task := NewGetEmailTask(GetEmailOptions{RequireConfirmationSet: true})
	tool := &updateEmailTool{task: task}

	out, err := tool.Execute(context.Background(), `{"email":"alex one hundred tree at example dot com"}`)
	if err != nil {
		t.Fatalf("Execute() error = %v, want hundred-group spoken digits accepted", err)
	}
	if out != "" {
		t.Fatalf("Execute() output = %q, want empty output after no-confirm completion", out)
	}

	select {
	case result := <-task.Result:
		if result.Email != "alex103@example.com" {
			t.Fatalf("Email = %q, want hundred-group spoken digits normalized", result.Email)
		}
	default:
		t.Fatal("task did not complete after hundred-group spoken email")
	}
}

func TestGetEmailTaskNormalizesSpokenHundredAndSingleDigitGroup(t *testing.T) {
	task := NewGetEmailTask(GetEmailOptions{RequireConfirmationSet: true})
	tool := &updateEmailTool{task: task}

	out, err := tool.Execute(context.Background(), `{"email":"alex one hundred and tree at example dot com"}`)
	if err != nil {
		t.Fatalf("Execute() error = %v, want hundred-and spoken digits accepted", err)
	}
	if out != "" {
		t.Fatalf("Execute() output = %q, want empty output after no-confirm completion", out)
	}

	select {
	case result := <-task.Result:
		if result.Email != "alex103@example.com" {
			t.Fatalf("Email = %q, want hundred-and spoken digits normalized", result.Email)
		}
	default:
		t.Fatal("task did not complete after hundred-and spoken email")
	}
}

func TestGetEmailTaskNormalizesSpokenHundredTensGroup(t *testing.T) {
	task := NewGetEmailTask(GetEmailOptions{RequireConfirmationSet: true})
	tool := &updateEmailTool{task: task}

	out, err := tool.Execute(context.Background(), `{"email":"alex one hundred twenty three at example dot com"}`)
	if err != nil {
		t.Fatalf("Execute() error = %v, want hundred-tens spoken digits accepted", err)
	}
	if out != "" {
		t.Fatalf("Execute() output = %q, want empty output after no-confirm completion", out)
	}

	select {
	case result := <-task.Result:
		if result.Email != "alex123@example.com" {
			t.Fatalf("Email = %q, want hundred-tens spoken digits normalized", result.Email)
		}
	default:
		t.Fatal("task did not complete after hundred-tens spoken email")
	}
}

func TestGetEmailTaskNormalizesRepeatedSpokenHundredGroup(t *testing.T) {
	task := NewGetEmailTask(GetEmailOptions{RequireConfirmationSet: true})
	tool := &updateEmailTool{task: task}

	out, err := tool.Execute(context.Background(), `{"email":"alex double one hundred tree at example dot com"}`)
	if err != nil {
		t.Fatalf("Execute() error = %v, want repeated hundred-group spoken digits accepted", err)
	}
	if out != "" {
		t.Fatalf("Execute() output = %q, want empty output after no-confirm completion", out)
	}

	select {
	case result := <-task.Result:
		if result.Email != "alex103103@example.com" {
			t.Fatalf("Email = %q, want repeated hundred-group spoken digits normalized", result.Email)
		}
	default:
		t.Fatal("task did not complete after repeated hundred-group spoken email")
	}
}

func TestGetEmailTaskNormalizesRepeatedSpokenHundredNaughtGroup(t *testing.T) {
	task := NewGetEmailTask(GetEmailOptions{RequireConfirmationSet: true})
	tool := &updateEmailTool{task: task}

	out, err := tool.Execute(context.Background(), `{"email":"alex double one hundred naught five at example dot com"}`)
	if err != nil {
		t.Fatalf("Execute() error = %v, want repeated hundred-naught spoken digits accepted", err)
	}
	if out != "" {
		t.Fatalf("Execute() output = %q, want empty output after no-confirm completion", out)
	}

	select {
	case result := <-task.Result:
		if result.Email != "alex105105@example.com" {
			t.Fatalf("Email = %q, want repeated hundred-naught group normalized", result.Email)
		}
	default:
		t.Fatal("task did not complete after repeated hundred-naught spoken email")
	}
}

func TestGetEmailTaskNormalizesTwentyAughtSpokenDigits(t *testing.T) {
	task := NewGetEmailTask(GetEmailOptions{RequireConfirmationSet: true})
	tool := &updateEmailTool{task: task}

	out, err := tool.Execute(context.Background(), `{"email":"alex twenty aught five at example dot com"}`)
	if err != nil {
		t.Fatalf("Execute() error = %v, want twenty-aught spoken digits accepted", err)
	}
	if out != "" {
		t.Fatalf("Execute() output = %q, want empty output after no-confirm completion", out)
	}

	select {
	case result := <-task.Result:
		if result.Email != "alex2005@example.com" {
			t.Fatalf("Email = %q, want twenty-aught spoken digits normalized", result.Email)
		}
	default:
		t.Fatal("task did not complete after twenty-aught spoken email")
	}
}

func TestGetEmailTaskNormalizesTwentyNaughtSpokenDigits(t *testing.T) {
	task := NewGetEmailTask(GetEmailOptions{RequireConfirmationSet: true})
	tool := &updateEmailTool{task: task}

	out, err := tool.Execute(context.Background(), `{"email":"alex twenty naught five at example dot com"}`)
	if err != nil {
		t.Fatalf("Execute() error = %v, want twenty-naught spoken digits accepted", err)
	}
	if out != "" {
		t.Fatalf("Execute() output = %q, want empty output after no-confirm completion", out)
	}

	select {
	case result := <-task.Result:
		if result.Email != "alex2005@example.com" {
			t.Fatalf("Email = %q, want twenty-naught spoken digits normalized", result.Email)
		}
	default:
		t.Fatal("task did not complete after twenty-naught spoken email")
	}
}

func TestGetEmailTaskNormalizesRepeatedSpokenDigits(t *testing.T) {
	task := NewGetEmailTask(GetEmailOptions{RequireConfirmationSet: true})
	tool := &updateEmailTool{task: task}

	out, err := tool.Execute(context.Background(), `{"email":"mike b double two at example dot com"}`)
	if err != nil {
		t.Fatalf("Execute() error = %v, want repeated spoken digits accepted", err)
	}
	if out != "" {
		t.Fatalf("Execute() output = %q, want empty output after no-confirm completion", out)
	}

	select {
	case result := <-task.Result:
		if result.Email != "mikeb22@example.com" {
			t.Fatalf("Email = %q, want repeated spoken digits normalized", result.Email)
		}
	default:
		t.Fatal("task did not complete after repeated spoken digit email")
	}
}

func TestGetEmailTaskNormalizesWonHomophone(t *testing.T) {
	task := NewGetEmailTask(GetEmailOptions{RequireConfirmationSet: true})
	tool := &updateEmailTool{task: task}

	out, err := tool.Execute(context.Background(), `{"email":"alex won at example dot com"}`)
	if err != nil {
		t.Fatalf("Execute() error = %v, want won homophone accepted", err)
	}
	if out != "" {
		t.Fatalf("Execute() output = %q, want empty output after no-confirm completion", out)
	}

	select {
	case result := <-task.Result:
		if result.Email != "alex1@example.com" {
			t.Fatalf("Email = %q, want won homophone normalized", result.Email)
		}
	default:
		t.Fatal("task did not complete after won-homophone email")
	}
}

func TestGetEmailTaskNormalizesQuadrupleSpokenDigits(t *testing.T) {
	task := NewGetEmailTask(GetEmailOptions{RequireConfirmationSet: true})
	tool := &updateEmailTool{task: task}

	out, err := tool.Execute(context.Background(), `{"email":"mike b quadruple two at example dot com"}`)
	if err != nil {
		t.Fatalf("Execute() error = %v, want quadruple spoken digits accepted", err)
	}
	if out != "" {
		t.Fatalf("Execute() output = %q, want empty output after no-confirm completion", out)
	}

	select {
	case result := <-task.Result:
		if result.Email != "mikeb2222@example.com" {
			t.Fatalf("Email = %q, want quadruple spoken digits normalized", result.Email)
		}
	default:
		t.Fatal("task did not complete after quadruple spoken digit email")
	}
}

func TestGetEmailTaskNormalizesSpokenDoubleLetterSpelling(t *testing.T) {
	task := NewGetEmailTask(GetEmailOptions{RequireConfirmationSet: true})
	tool := &updateEmailTool{task: task}

	out, err := tool.Execute(context.Background(), `{"email":"will w i double l at example dot com"}`)
	if err != nil {
		t.Fatalf("Execute() error = %v, want spoken double-letter spelling accepted", err)
	}
	if out != "" {
		t.Fatalf("Execute() output = %q, want empty output after no-confirm completion", out)
	}

	select {
	case result := <-task.Result:
		if result.Email != "will@example.com" {
			t.Fatalf("Email = %q, want spoken double-letter spelling normalized", result.Email)
		}
	default:
		t.Fatal("task did not complete after spoken double-letter spelling email")
	}
}

func TestGetEmailTaskNormalizesSpokenDoubleYouLetterSpelling(t *testing.T) {
	task := NewGetEmailTask(GetEmailOptions{RequireConfirmationSet: true})
	tool := &updateEmailTool{task: task}

	out, err := tool.Execute(context.Background(), `{"email":"will double you i double l at example dot com"}`)
	if err != nil {
		t.Fatalf("Execute() error = %v, want spoken double-you W spelling accepted", err)
	}
	if out != "" {
		t.Fatalf("Execute() output = %q, want empty output after no-confirm completion", out)
	}

	select {
	case result := <-task.Result:
		if result.Email != "will@example.com" {
			t.Fatalf("Email = %q, want double-you spelling normalized", result.Email)
		}
	default:
		t.Fatal("task did not complete after double-you spoken email spelling")
	}
}

func TestGetEmailTaskNormalizesSpokenDoubleEweLetterSpelling(t *testing.T) {
	task := NewGetEmailTask(GetEmailOptions{RequireConfirmationSet: true})
	tool := &updateEmailTool{task: task}

	out, err := tool.Execute(context.Background(), `{"email":"will double ewe i double l at example dot com"}`)
	if err != nil {
		t.Fatalf("Execute() error = %v, want spoken double-ewe W spelling accepted", err)
	}
	if out != "" {
		t.Fatalf("Execute() output = %q, want empty output after no-confirm completion", out)
	}

	select {
	case result := <-task.Result:
		if result.Email != "will@example.com" {
			t.Fatalf("Email = %q, want double-ewe spelling normalized", result.Email)
		}
	default:
		t.Fatal("task did not complete after double-ewe spoken email spelling")
	}
}

func TestGetEmailTaskNormalizesSpokenSingleLetterSpelling(t *testing.T) {
	task := NewGetEmailTask(GetEmailOptions{RequireConfirmationSet: true})
	tool := &updateEmailTool{task: task}

	out, err := tool.Execute(context.Background(), `{"email":"ada a single d a at example dot com"}`)
	if err != nil {
		t.Fatalf("Execute() error = %v, want spoken single-letter spelling accepted", err)
	}
	if out != "" {
		t.Fatalf("Execute() output = %q, want empty output after no-confirm completion", out)
	}

	select {
	case result := <-task.Result:
		if result.Email != "ada@example.com" {
			t.Fatalf("Email = %q, want spoken single-letter spelling normalized", result.Email)
		}
	default:
		t.Fatal("task did not complete after spoken single-letter spelling email")
	}
}

func TestGetEmailTaskFiltersSpokenFiller(t *testing.T) {
	task := NewGetEmailTask(GetEmailOptions{RequireConfirmationSet: true})
	tool := &updateEmailTool{task: task}

	out, err := tool.Execute(context.Background(), `{"email":"um my email is ada at example dot com"}`)
	if err != nil {
		t.Fatalf("Execute() error = %v, want spoken filler accepted", err)
	}
	if out != "" {
		t.Fatalf("Execute() output = %q, want empty output after no-confirm completion", out)
	}

	select {
	case result := <-task.Result:
		if result.Email != "ada@example.com" {
			t.Fatalf("Email = %q, want filler words filtered", result.Email)
		}
	default:
		t.Fatal("task did not complete after filler-spoken email")
	}
}

func TestGetEmailTaskFiltersArticlePreamble(t *testing.T) {
	task := NewGetEmailTask(GetEmailOptions{RequireConfirmationSet: true})
	tool := &updateEmailTool{task: task}

	_, err := tool.Execute(context.Background(), `{"email":"the email is ada at example dot com"}`)
	if err != nil {
		t.Fatalf("Execute() error = %v, want article email preamble accepted", err)
	}

	select {
	case result := <-task.Result:
		if result.Email != "ada@example.com" {
			t.Fatalf("Email = %q, want article preamble filtered", result.Email)
		}
	default:
		t.Fatal("task did not complete after article-preamble email")
	}
}

func TestGetEmailTaskFiltersSpokenEMailPreamble(t *testing.T) {
	task := NewGetEmailTask(GetEmailOptions{RequireConfirmationSet: true})
	tool := &updateEmailTool{task: task}

	_, err := tool.Execute(context.Background(), `{"email":"e mail is ada at example dot com"}`)
	if err != nil {
		t.Fatalf("Execute() error = %v, want split e-mail preamble accepted", err)
	}

	select {
	case result := <-task.Result:
		if result.Email != "ada@example.com" {
			t.Fatalf("Email = %q, want split e-mail preamble filtered", result.Email)
		}
	default:
		t.Fatal("task did not complete after split e-mail preamble")
	}
}

func TestGetEmailTaskFiltersHyphenatedEmailPreamble(t *testing.T) {
	task := NewGetEmailTask(GetEmailOptions{RequireConfirmationSet: true})
	tool := &updateEmailTool{task: task}

	_, err := tool.Execute(context.Background(), `{"email":"e-mail is ada at example dot com"}`)
	if err != nil {
		t.Fatalf("Execute() error = %v, want hyphenated e-mail preamble accepted", err)
	}

	select {
	case result := <-task.Result:
		if result.Email != "ada@example.com" {
			t.Fatalf("Email = %q, want hyphenated e-mail preamble filtered", result.Email)
		}
	default:
		t.Fatal("task did not complete after hyphenated e-mail preamble")
	}
}

func TestGetEmailTaskFiltersContractedEmailPreamble(t *testing.T) {
	task := NewGetEmailTask(GetEmailOptions{RequireConfirmationSet: true})
	tool := &updateEmailTool{task: task}

	_, err := tool.Execute(context.Background(), `{"email":"email's ada at example dot com"}`)
	if err != nil {
		t.Fatalf("Execute() error = %v, want contracted email preamble accepted", err)
	}

	select {
	case result := <-task.Result:
		if result.Email != "ada@example.com" {
			t.Fatalf("Email = %q, want contracted email preamble filtered", result.Email)
		}
	default:
		t.Fatal("task did not complete after contracted email preamble")
	}
}

func TestGetEmailTaskFiltersSplitContractedEmailPreamble(t *testing.T) {
	task := NewGetEmailTask(GetEmailOptions{RequireConfirmationSet: true})
	tool := &updateEmailTool{task: task}

	_, err := tool.Execute(context.Background(), `{"email":"email s ada at example dot com"}`)
	if err != nil {
		t.Fatalf("Execute() error = %v, want split contracted email preamble accepted", err)
	}

	select {
	case result := <-task.Result:
		if result.Email != "ada@example.com" {
			t.Fatalf("Email = %q, want split contracted email preamble filtered", result.Email)
		}
	default:
		t.Fatal("task did not complete after split contracted email preamble")
	}
}

func TestGetEmailTaskFiltersContractedHyphenatedEmailPreamble(t *testing.T) {
	task := NewGetEmailTask(GetEmailOptions{RequireConfirmationSet: true})
	tool := &updateEmailTool{task: task}

	_, err := tool.Execute(context.Background(), `{"email":"e-mail's ada at example dot com"}`)
	if err != nil {
		t.Fatalf("Execute() error = %v, want contracted hyphenated email preamble accepted", err)
	}

	select {
	case result := <-task.Result:
		if result.Email != "ada@example.com" {
			t.Fatalf("Email = %q, want contracted hyphenated email preamble filtered", result.Email)
		}
	default:
		t.Fatal("task did not complete after contracted hyphenated email preamble")
	}
}

func TestGetEmailTaskFiltersWillBePreambleAfterEmailMeta(t *testing.T) {
	task := NewGetEmailTask(GetEmailOptions{RequireConfirmationSet: true})
	tool := &updateEmailTool{task: task}

	_, err := tool.Execute(context.Background(), `{"email":"my email will be ada at example dot com"}`)
	if err != nil {
		t.Fatalf("Execute() error = %v, want will-be email preamble accepted", err)
	}

	select {
	case result := <-task.Result:
		if result.Email != "ada@example.com" {
			t.Fatalf("Email = %q, want will-be preamble filtered", result.Email)
		}
	default:
		t.Fatal("task did not complete after will-be email preamble")
	}
}

func TestGetEmailTaskFiltersNoSpaceFiller(t *testing.T) {
	task := NewGetEmailTask(GetEmailOptions{RequireConfirmationSet: true})
	tool := &updateEmailTool{task: task}

	_, err := tool.Execute(context.Background(), `{"email":"no space ada at example dot com"}`)
	if err != nil {
		t.Fatalf("Execute() error = %v, want no-space filler accepted", err)
	}

	select {
	case result := <-task.Result:
		if result.Email != "ada@example.com" {
			t.Fatalf("Email = %q, want no-space filler filtered", result.Email)
		}
	default:
		t.Fatal("task did not complete after no-space filler email")
	}
}

func TestGetEmailTaskFiltersLowercaseFiller(t *testing.T) {
	task := NewGetEmailTask(GetEmailOptions{RequireConfirmationSet: true})
	tool := &updateEmailTool{task: task}

	_, err := tool.Execute(context.Background(), `{"email":"all lower case ada at example dot com"}`)
	if err != nil {
		t.Fatalf("Execute() error = %v, want lowercase filler accepted", err)
	}

	select {
	case result := <-task.Result:
		if result.Email != "ada@example.com" {
			t.Fatalf("Email = %q, want lowercase filler filtered", result.Email)
		}
	default:
		t.Fatal("task did not complete after lowercase filler email")
	}
}

func TestGetEmailTaskFiltersCapsFiller(t *testing.T) {
	task := NewGetEmailTask(GetEmailOptions{RequireConfirmationSet: true})
	tool := &updateEmailTool{task: task}

	_, err := tool.Execute(context.Background(), `{"email":"all caps ada at example dot com"}`)
	if err != nil {
		t.Fatalf("Execute() error = %v, want caps filler accepted", err)
	}

	select {
	case result := <-task.Result:
		if result.Email != "ada@example.com" {
			t.Fatalf("Email = %q, want caps filler filtered", result.Email)
		}
	default:
		t.Fatal("task did not complete after caps filler email")
	}
}

func TestGetEmailTaskFiltersLikeSpokenFiller(t *testing.T) {
	task := NewGetEmailTask(GetEmailOptions{RequireConfirmationSet: true})
	tool := &updateEmailTool{task: task}

	out, err := tool.Execute(context.Background(), `{"email":"like my email is ada at example dot com"}`)
	if err != nil {
		t.Fatalf("Execute() error = %v, want like filler accepted", err)
	}
	if out != "" {
		t.Fatalf("Execute() output = %q, want empty output after no-confirm completion", out)
	}

	select {
	case result := <-task.Result:
		if result.Email != "ada@example.com" {
			t.Fatalf("Email = %q, want like filler words filtered", result.Email)
		}
	default:
		t.Fatal("task did not complete after like-filler-spoken email")
	}
}

func TestGetEmailTaskFiltersActuallySpokenFiller(t *testing.T) {
	task := NewGetEmailTask(GetEmailOptions{RequireConfirmationSet: true})
	tool := &updateEmailTool{task: task}

	out, err := tool.Execute(context.Background(), `{"email":"actually my email is ada at example dot com"}`)
	if err != nil {
		t.Fatalf("Execute() error = %v, want correction filler accepted", err)
	}
	if out != "" {
		t.Fatalf("Execute() output = %q, want empty output after no-confirm completion", out)
	}

	select {
	case result := <-task.Result:
		if result.Email != "ada@example.com" {
			t.Fatalf("Email = %q, want correction filler words filtered", result.Email)
		}
	default:
		t.Fatal("task did not complete after correction-filler-spoken email")
	}
}

func TestGetEmailTaskFiltersSorrySpokenFiller(t *testing.T) {
	task := NewGetEmailTask(GetEmailOptions{RequireConfirmationSet: true})
	tool := &updateEmailTool{task: task}

	out, err := tool.Execute(context.Background(), `{"email":"sorry my email is ada at example dot com"}`)
	if err != nil {
		t.Fatalf("Execute() error = %v, want apology filler accepted", err)
	}
	if out != "" {
		t.Fatalf("Execute() output = %q, want empty output after no-confirm completion", out)
	}

	select {
	case result := <-task.Result:
		if result.Email != "ada@example.com" {
			t.Fatalf("Email = %q, want apology filler words filtered", result.Email)
		}
	default:
		t.Fatal("task did not complete after apology-filler-spoken email")
	}
}

func TestGetEmailTaskFiltersTrailingSpokenFiller(t *testing.T) {
	task := NewGetEmailTask(GetEmailOptions{RequireConfirmationSet: true})
	tool := &updateEmailTool{task: task}

	out, err := tool.Execute(context.Background(), `{"email":"ada at example dot com please"}`)
	if err != nil {
		t.Fatalf("Execute() error = %v, want trailing spoken filler accepted", err)
	}
	if out != "" {
		t.Fatalf("Execute() output = %q, want empty output after no-confirm completion", out)
	}

	select {
	case result := <-task.Result:
		if result.Email != "ada@example.com" {
			t.Fatalf("Email = %q, want trailing filler words filtered", result.Email)
		}
	default:
		t.Fatal("task did not complete after trailing filler-spoken email")
	}
}

func TestGetEmailTaskFiltersTrailingDoneFiller(t *testing.T) {
	task := NewGetEmailTask(GetEmailOptions{RequireConfirmationSet: true})
	tool := &updateEmailTool{task: task}

	out, err := tool.Execute(context.Background(), `{"email":"ada at example dot com done"}`)
	if err != nil {
		t.Fatalf("Execute() error = %v, want trailing done filler accepted", err)
	}
	if out != "" {
		t.Fatalf("Execute() output = %q, want empty output after no-confirm completion", out)
	}

	select {
	case result := <-task.Result:
		if result.Email != "ada@example.com" {
			t.Fatalf("Email = %q, want trailing done filler filtered", result.Email)
		}
	default:
		t.Fatal("task did not complete after trailing-done-spoken email")
	}
}

func TestGetEmailTaskFiltersTrailingAllDoneFiller(t *testing.T) {
	task := NewGetEmailTask(GetEmailOptions{RequireConfirmationSet: true})
	tool := &updateEmailTool{task: task}

	out, err := tool.Execute(context.Background(), `{"email":"ada at example dot com all done"}`)
	if err != nil {
		t.Fatalf("Execute() error = %v, want trailing all-done filler accepted", err)
	}
	if out != "" {
		t.Fatalf("Execute() output = %q, want empty output after no-confirm completion", out)
	}

	select {
	case result := <-task.Result:
		if result.Email != "ada@example.com" {
			t.Fatalf("Email = %q, want trailing all-done filler filtered", result.Email)
		}
	default:
		t.Fatal("task did not complete after trailing-all-done-spoken email")
	}
}

func TestGetEmailTaskFiltersTrailingCompletionPhrase(t *testing.T) {
	task := NewGetEmailTask(GetEmailOptions{RequireConfirmationSet: true})
	tool := &updateEmailTool{task: task}

	out, err := tool.Execute(context.Background(), `{"email":"ada at example dot com that's it"}`)
	if err != nil {
		t.Fatalf("Execute() error = %v, want trailing completion phrase accepted", err)
	}
	if out != "" {
		t.Fatalf("Execute() output = %q, want empty output after no-confirm completion", out)
	}

	select {
	case result := <-task.Result:
		if result.Email != "ada@example.com" {
			t.Fatalf("Email = %q, want trailing completion phrase filtered", result.Email)
		}
	default:
		t.Fatal("task did not complete after trailing completion phrase email")
	}
}

func TestGetEmailTaskFiltersTrailingExpandedCompletionPhrase(t *testing.T) {
	task := NewGetEmailTask(GetEmailOptions{RequireConfirmationSet: true})
	tool := &updateEmailTool{task: task}

	out, err := tool.Execute(context.Background(), `{"email":"ada at example dot com that is all"}`)
	if err != nil {
		t.Fatalf("Execute() error = %v, want trailing expanded completion phrase accepted", err)
	}
	if out != "" {
		t.Fatalf("Execute() output = %q, want empty output after no-confirm completion", out)
	}

	select {
	case result := <-task.Result:
		if result.Email != "ada@example.com" {
			t.Fatalf("Email = %q, want trailing expanded completion phrase filtered", result.Email)
		}
	default:
		t.Fatal("task did not complete after trailing expanded completion phrase email")
	}
}

func TestGetEmailTaskFiltersTrailingThatWillBeAllPhrase(t *testing.T) {
	task := NewGetEmailTask(GetEmailOptions{RequireConfirmationSet: true})
	tool := &updateEmailTool{task: task}

	out, err := tool.Execute(context.Background(), `{"email":"ada at example dot com that'll be all"}`)
	if err != nil {
		t.Fatalf("Execute() error = %v, want trailing that'll-be completion phrase accepted", err)
	}
	if out != "" {
		t.Fatalf("Execute() output = %q, want empty output after no-confirm completion", out)
	}

	select {
	case result := <-task.Result:
		if result.Email != "ada@example.com" {
			t.Fatalf("Email = %q, want trailing that'll-be completion phrase filtered", result.Email)
		}
	default:
		t.Fatal("task did not complete after trailing that'll-be completion phrase email")
	}
}

func TestGetEmailTaskFiltersTrailingExpandedThatWillBeAllPhrase(t *testing.T) {
	task := NewGetEmailTask(GetEmailOptions{RequireConfirmationSet: true})
	tool := &updateEmailTool{task: task}

	out, err := tool.Execute(context.Background(), `{"email":"ada at example dot com that will be all"}`)
	if err != nil {
		t.Fatalf("Execute() error = %v, want trailing expanded that-will-be completion phrase accepted", err)
	}
	if out != "" {
		t.Fatalf("Execute() output = %q, want empty output after no-confirm completion", out)
	}

	select {
	case result := <-task.Result:
		if result.Email != "ada@example.com" {
			t.Fatalf("Email = %q, want trailing expanded that-will-be completion phrase filtered", result.Email)
		}
	default:
		t.Fatal("task did not complete after trailing expanded that-will-be completion phrase email")
	}
}

func TestGetEmailTaskFiltersTrailingSplitThatllShortPhrase(t *testing.T) {
	task := NewGetEmailTask(GetEmailOptions{RequireConfirmationSet: true})
	tool := &updateEmailTool{task: task}

	out, err := tool.Execute(context.Background(), `{"email":"ada at example dot com that ll be all"}`)
	if err != nil {
		t.Fatalf("Execute() error = %v, want split contracted trailing completion phrase accepted", err)
	}
	if out != "" {
		t.Fatalf("Execute() output = %q, want empty output after no-confirm completion", out)
	}

	select {
	case result := <-task.Result:
		if result.Email != "ada@example.com" {
			t.Fatalf("Email = %q, want split contracted trailing completion phrase filtered", result.Email)
		}
	default:
		t.Fatal("task did not complete after split contracted trailing completion phrase email")
	}
}

func TestGetEmailTaskFiltersTrailingThatsAllPhrase(t *testing.T) {
	task := NewGetEmailTask(GetEmailOptions{RequireConfirmationSet: true})
	tool := &updateEmailTool{task: task}

	out, err := tool.Execute(context.Background(), `{"email":"ada at example dot com that's all"}`)
	if err != nil {
		t.Fatalf("Execute() error = %v, want trailing that's-all phrase accepted", err)
	}
	if out != "" {
		t.Fatalf("Execute() output = %q, want empty output after no-confirm completion", out)
	}

	select {
	case result := <-task.Result:
		if result.Email != "ada@example.com" {
			t.Fatalf("Email = %q, want trailing that's-all phrase filtered", result.Email)
		}
	default:
		t.Fatal("task did not complete after trailing that's-all phrase email")
	}
}

func TestGetEmailTaskFiltersTrailingForNowThanksPhrase(t *testing.T) {
	task := NewGetEmailTask(GetEmailOptions{RequireConfirmationSet: true})
	tool := &updateEmailTool{task: task}

	out, err := tool.Execute(context.Background(), `{"email":"ada at example dot com that's it for now thanks"}`)
	if err != nil {
		t.Fatalf("Execute() error = %v, want trailing for-now-thanks phrase accepted", err)
	}
	if out != "" {
		t.Fatalf("Execute() output = %q, want empty output after no-confirm completion", out)
	}

	select {
	case result := <-task.Result:
		if result.Email != "ada@example.com" {
			t.Fatalf("Email = %q, want trailing for-now-thanks phrase filtered", result.Email)
		}
	default:
		t.Fatal("task did not complete after trailing for-now-thanks phrase email")
	}
}

func TestGetEmailTaskFiltersTrailingForTodayThanksPhrase(t *testing.T) {
	task := NewGetEmailTask(GetEmailOptions{RequireConfirmationSet: true})
	tool := &updateEmailTool{task: task}

	out, err := tool.Execute(context.Background(), `{"email":"ada at example dot com that's it for today thanks"}`)
	if err != nil {
		t.Fatalf("Execute() error = %v, want trailing for-today-thanks phrase accepted", err)
	}
	if out != "" {
		t.Fatalf("Execute() output = %q, want empty output after no-confirm completion", out)
	}

	select {
	case result := <-task.Result:
		if result.Email != "ada@example.com" {
			t.Fatalf("Email = %q, want trailing for-today-thanks phrase filtered", result.Email)
		}
	default:
		t.Fatal("task did not complete after trailing for-today-thanks phrase email")
	}
}

func TestGetEmailTaskFiltersTrailingForYouPhrase(t *testing.T) {
	task := NewGetEmailTask(GetEmailOptions{RequireConfirmationSet: true})
	tool := &updateEmailTool{task: task}

	out, err := tool.Execute(context.Background(), `{"email":"ada at example dot com that's all for you"}`)
	if err != nil {
		t.Fatalf("Execute() error = %v, want trailing for-you phrase accepted", err)
	}
	if out != "" {
		t.Fatalf("Execute() output = %q, want empty output after no-confirm completion", out)
	}

	select {
	case result := <-task.Result:
		if result.Email != "ada@example.com" {
			t.Fatalf("Email = %q, want trailing for-you phrase filtered", result.Email)
		}
	default:
		t.Fatal("task did not complete after trailing for-you phrase email")
	}
}

func TestGetEmailTaskFiltersTrailingShortForYouThanksPhrase(t *testing.T) {
	task := NewGetEmailTask(GetEmailOptions{RequireConfirmationSet: true})
	tool := &updateEmailTool{task: task}

	out, err := tool.Execute(context.Background(), `{"email":"ada at example dot com for you thanks"}`)
	if err != nil {
		t.Fatalf("Execute() error = %v, want trailing short for-you-thanks phrase accepted", err)
	}
	if out != "" {
		t.Fatalf("Execute() output = %q, want empty output after no-confirm completion", out)
	}

	select {
	case result := <-task.Result:
		if result.Email != "ada@example.com" {
			t.Fatalf("Email = %q, want trailing short for-you-thanks phrase filtered", result.Email)
		}
	default:
		t.Fatal("task did not complete after trailing short for-you-thanks phrase email")
	}
}

func TestGetEmailTaskFiltersTrailingShortForTodayThanksPhrase(t *testing.T) {
	task := NewGetEmailTask(GetEmailOptions{RequireConfirmationSet: true})
	tool := &updateEmailTool{task: task}

	out, err := tool.Execute(context.Background(), `{"email":"ada at example dot com for today thanks"}`)
	if err != nil {
		t.Fatalf("Execute() error = %v, want trailing short for-today-thanks phrase accepted", err)
	}
	if out != "" {
		t.Fatalf("Execute() output = %q, want empty output after no-confirm completion", out)
	}

	select {
	case result := <-task.Result:
		if result.Email != "ada@example.com" {
			t.Fatalf("Email = %q, want trailing short for-today-thanks phrase filtered", result.Email)
		}
	default:
		t.Fatal("task did not complete after trailing short for-today-thanks phrase email")
	}
}

func TestGetEmailTaskFiltersTrailingShortForTheDayThanksPhrase(t *testing.T) {
	task := NewGetEmailTask(GetEmailOptions{RequireConfirmationSet: true})
	tool := &updateEmailTool{task: task}

	out, err := tool.Execute(context.Background(), `{"email":"ada at example dot com for the day thanks"}`)
	if err != nil {
		t.Fatalf("Execute() error = %v, want trailing short for-the-day-thanks phrase accepted", err)
	}
	if out != "" {
		t.Fatalf("Execute() output = %q, want empty output after no-confirm completion", out)
	}

	select {
	case result := <-task.Result:
		if result.Email != "ada@example.com" {
			t.Fatalf("Email = %q, want trailing short for-the-day-thanks phrase filtered", result.Email)
		}
	default:
		t.Fatal("task did not complete after trailing short for-the-day-thanks phrase email")
	}
}

func TestGetEmailTaskFiltersTrailingForTheDayThanksPhrase(t *testing.T) {
	task := NewGetEmailTask(GetEmailOptions{RequireConfirmationSet: true})
	tool := &updateEmailTool{task: task}

	out, err := tool.Execute(context.Background(), `{"email":"ada at example dot com that's it for the day thanks"}`)
	if err != nil {
		t.Fatalf("Execute() error = %v, want trailing for-the-day-thanks phrase accepted", err)
	}
	if out != "" {
		t.Fatalf("Execute() output = %q, want empty output after no-confirm completion", out)
	}

	select {
	case result := <-task.Result:
		if result.Email != "ada@example.com" {
			t.Fatalf("Email = %q, want trailing for-the-day-thanks phrase filtered", result.Email)
		}
	default:
		t.Fatal("task did not complete after trailing for-the-day-thanks phrase email")
	}
}

func TestGetEmailTaskFiltersTrailingExpandedThatWillBeAllForTheDayThanksPhrase(t *testing.T) {
	task := NewGetEmailTask(GetEmailOptions{RequireConfirmationSet: true})
	tool := &updateEmailTool{task: task}

	out, err := tool.Execute(context.Background(), `{"email":"ada at example dot com that will be all for the day thanks"}`)
	if err != nil {
		t.Fatalf("Execute() error = %v, want expanded that-will-be for-the-day phrase accepted", err)
	}
	if out != "" {
		t.Fatalf("Execute() output = %q, want empty output after no-confirm completion", out)
	}

	select {
	case result := <-task.Result:
		if result.Email != "ada@example.com" {
			t.Fatalf("Email = %q, want expanded that-will-be for-the-day phrase filtered", result.Email)
		}
	default:
		t.Fatal("task did not complete after expanded that-will-be for-the-day phrase email")
	}
}

func TestGetEmailTaskFiltersTrailingThatllBeAllForTheDayThanksPhrase(t *testing.T) {
	task := NewGetEmailTask(GetEmailOptions{RequireConfirmationSet: true})
	tool := &updateEmailTool{task: task}

	out, err := tool.Execute(context.Background(), `{"email":"ada at example dot com that'll be all for the day thanks"}`)
	if err != nil {
		t.Fatalf("Execute() error = %v, want contracted that'll-be for-the-day phrase accepted", err)
	}
	if out != "" {
		t.Fatalf("Execute() output = %q, want empty output after no-confirm completion", out)
	}

	select {
	case result := <-task.Result:
		if result.Email != "ada@example.com" {
			t.Fatalf("Email = %q, want contracted that'll-be for-the-day phrase filtered", result.Email)
		}
	default:
		t.Fatal("task did not complete after contracted that'll-be for-the-day phrase email")
	}
}

func TestGetEmailTaskFiltersTrailingThatllBeAllForDayThanksPhrase(t *testing.T) {
	task := NewGetEmailTask(GetEmailOptions{RequireConfirmationSet: true})
	tool := &updateEmailTool{task: task}

	out, err := tool.Execute(context.Background(), `{"email":"ada at example dot com that'll be all for day thanks"}`)
	if err != nil {
		t.Fatalf("Execute() error = %v, want contracted that'll-be for-day phrase accepted", err)
	}
	if out != "" {
		t.Fatalf("Execute() output = %q, want empty output after no-confirm completion", out)
	}

	select {
	case result := <-task.Result:
		if result.Email != "ada@example.com" {
			t.Fatalf("Email = %q, want contracted that'll-be for-day phrase filtered", result.Email)
		}
	default:
		t.Fatal("task did not complete after contracted that'll-be for-day phrase email")
	}
}

func TestGetEmailTaskFiltersTrailingSplitThatllBeAllForDayThanksPhrase(t *testing.T) {
	task := NewGetEmailTask(GetEmailOptions{RequireConfirmationSet: true})
	tool := &updateEmailTool{task: task}

	out, err := tool.Execute(context.Background(), `{"email":"ada at example dot com that ll be all for day thanks"}`)
	if err != nil {
		t.Fatalf("Execute() error = %v, want split contracted that'll-be for-day phrase accepted", err)
	}
	if out != "" {
		t.Fatalf("Execute() output = %q, want empty output after no-confirm completion", out)
	}

	select {
	case result := <-task.Result:
		if result.Email != "ada@example.com" {
			t.Fatalf("Email = %q, want split contracted that'll-be for-day phrase filtered", result.Email)
		}
	default:
		t.Fatal("task did not complete after split contracted that'll-be for-day phrase email")
	}
}

func TestGetEmailTaskRejectsInvalidEmail(t *testing.T) {
	task := NewGetEmailTask(GetEmailOptions{RequireConfirmationSet: true})
	tool := &updateEmailTool{task: task}

	_, err := tool.Execute(context.Background(), `{"email":"ada at example"}`)
	if err == nil {
		t.Fatal("Execute() error = nil, want invalid email error")
	}
	if !strings.Contains(err.Error(), "Invalid email address provided") {
		t.Fatalf("Execute() error = %v, want invalid email", err)
	}

	select {
	case result := <-task.Result:
		t.Fatalf("task completed with %#v, want no completion for invalid email", result)
	default:
	}
}

func TestGetEmailTaskOptionsSeedReferenceChatContext(t *testing.T) {
	chatCtx := llm.NewChatContext()
	chatCtx.Append(&llm.ChatMessage{
		Role:    llm.ChatRoleUser,
		Content: []llm.ChatContent{{Text: "my email is ada at example dot com"}},
		ID:      "prior-email",
	})

	opts := GetEmailOptions{}
	field := reflect.ValueOf(&opts).Elem().FieldByName("ChatContext")
	if !field.IsValid() {
		t.Fatal("GetEmailOptions.ChatContext missing; want reference chat_ctx constructor option")
	}
	field.Set(reflect.ValueOf(chatCtx))

	task := NewGetEmailTask(opts)
	if task.ChatCtx == nil {
		t.Fatal("task ChatCtx = nil, want constructor chat context copy")
	}
	if task.ChatCtx == chatCtx {
		t.Fatal("task ChatCtx aliases constructor context, want reference-style copy")
	}
	if task.ChatCtx.GetByID("prior-email") == nil {
		t.Fatalf("task ChatCtx items = %#v, want constructor chat item", task.ChatCtx.Items)
	}
}

func TestGetEmailTaskPreservesReferenceExtraTools(t *testing.T) {
	opts := GetEmailOptions{}
	field := reflect.ValueOf(&opts).Elem().FieldByName("Tools")
	if !field.IsValid() {
		t.Fatal("GetEmailOptions.Tools missing; want reference tools constructor option")
	}
	field.Set(reflect.ValueOf([]llm.Tool{referenceEmailExtraTool{id: "email_help"}}))

	task := NewGetEmailTask(opts)

	if len(task.Agent.Tools) < 2 {
		t.Fatalf("tools = %#v, want extra tool then update/decline tools", task.Agent.Tools)
	}
	if got := task.Agent.Tools[0].Name(); got != "email_help" {
		t.Fatalf("first tool = %q, want reference extra tool before update tool", got)
	}
	if got := task.Agent.Tools[1].Name(); got != "update_email_address" {
		t.Fatalf("second tool = %q, want update_email_address after extra tools", got)
	}
}

func TestGetEmailTaskExplicitAskIgnoresUpdateToolOnEnter(t *testing.T) {
	task := NewGetEmailTask(GetEmailOptions{RequireExplicitAsk: true})
	tool := task.Agent.Tools[0]

	if !llm.ToolHasFlag(tool, llm.ToolFlagIgnoreOnEnter) {
		t.Fatalf("%s ToolFlags missing ToolFlagIgnoreOnEnter when RequireExplicitAsk is true", tool.Name())
	}
}

func TestGetEmailTaskInjectsConfirmToolAfterUpdate(t *testing.T) {
	task := NewGetEmailTask(GetEmailOptions{})
	if len(task.Agent.Tools) != 2 {
		t.Fatalf("initial tools = %d, want update/decline before email is captured", len(task.Agent.Tools))
	}

	update := &updateEmailTool{task: task}
	out, err := update.Execute(context.Background(), `{"email":"ada@example.com"}`)
	if err != nil {
		t.Fatalf("update Execute() error = %v", err)
	}
	if out == "" {
		t.Fatal("update Execute() output is empty, want confirmation guidance")
	}
	if strings.Contains(out, "ada@example.com") {
		t.Fatalf("update Execute() output = %q, want no raw contiguous email echo", out)
	}
	if strings.Contains(out, "a d a @ e x a m p l e . c o m") {
		t.Fatalf("update Execute() output = %q, want no character-by-character email echo", out)
	}
	want := "The email has been updated.\nAsk the user to confirm the updated email address without repeating it back.\nPrompt the user for confirmation, do not call `confirm_email_address` directly"
	if out != want {
		t.Fatalf("update Execute() output = %q, want %q", out, want)
	}
	if len(task.Agent.Tools) != 3 || task.Agent.Tools[2].Name() != "confirm_email_address" {
		t.Fatalf("tools = %#v, want confirm_email_address appended", task.Agent.Tools)
	}

	confirm := &confirmEmailTool{task: task, email: "ada@example.com"}
	confirmOut, err := confirm.Execute(context.Background(), `{}`)
	if err != nil {
		t.Fatalf("confirm Execute() error = %v", err)
	}
	if confirmOut != "" {
		t.Fatalf("confirm Execute() output = %q, want empty output after completion", confirmOut)
	}

	select {
	case result := <-task.Result:
		if result.Email != "ada@example.com" {
			t.Fatalf("Email = %q, want ada@example.com", result.Email)
		}
	default:
		t.Fatal("task did not complete after confirmation")
	}
}

func TestConfirmEmailToolUsesReferenceSchema(t *testing.T) {
	task := NewGetEmailTask(GetEmailOptions{})
	tool := &confirmEmailTool{task: task, email: "ada@example.com"}

	wantDescription := "Call after the user confirms the email address is correct."
	if got := tool.Description(); got != wantDescription {
		t.Fatalf("confirm_email_address description = %q, want %q", got, wantDescription)
	}
	properties, ok := tool.Parameters()["properties"].(map[string]any)
	if !ok {
		t.Fatalf("properties = %#v, want map", tool.Parameters()["properties"])
	}
	if len(properties) != 0 {
		t.Fatalf("properties = %#v, want empty parameter schema", properties)
	}
}

func TestGetEmailTaskCanDisableDefaultConfirmation(t *testing.T) {
	task := NewGetEmailTask(GetEmailOptions{RequireConfirmation: false, RequireConfirmationSet: true})
	update := &updateEmailTool{task: task}

	out, err := update.Execute(context.Background(), `{"email":"ada@example.com"}`)
	if err != nil {
		t.Fatalf("update Execute() error = %v", err)
	}
	if out != "" {
		t.Fatalf("update Execute() output = %q, want empty output after no-confirm completion", out)
	}
}

func TestGetEmailTaskDefaultConfirmationUsesInputModality(t *testing.T) {
	textCtx := agent.WithRunContext(
		context.Background(),
		agent.NewRunContext(nil, agent.NewSpeechHandle(true, agent.InputDetails{Modality: "text"}), nil),
	)
	task := NewGetEmailTask(GetEmailOptions{})
	update := &updateEmailTool{task: task}

	out, err := update.Execute(textCtx, `{"email":"ada@example.com"}`)
	if err != nil {
		t.Fatalf("update Execute() error = %v", err)
	}
	if out != "" {
		t.Fatalf("update Execute() output = %q, want direct text completion without confirmation prompt", out)
	}

	select {
	case result := <-task.Result:
		if result.Email != "ada@example.com" {
			t.Fatalf("Email = %q, want direct text completion", result.Email)
		}
	default:
		t.Fatal("task did not complete for text input")
	}
}

func TestGetEmailTaskInstructionsOmitConfirmationWhenDisabled(t *testing.T) {
	task := NewGetEmailTask(GetEmailOptions{RequireConfirmation: false, RequireConfirmationSet: true})

	if strings.Contains(task.Instructions, "confirm_email_address") {
		t.Fatalf("Instructions = %q, want no confirm_email_address guidance when confirmation disabled", task.Instructions)
	}
}

func TestGetEmailTaskInstructionsUseReferenceToolGuidance(t *testing.T) {
	task := NewGetEmailTask(GetEmailOptions{})

	for _, want := range []string{
		"Call `update_email_address` at the first opportunity whenever you form a new hypothesis about the email. (before asking any questions or providing any answers.)",
		"Always explicitly invoke a tool when applicable. Do not simulate tool usage, no real action is taken unless the tool is explicitly called.",
	} {
		if !strings.Contains(task.Instructions, want) {
			t.Fatalf("Instructions = %q, want reference guidance %q", task.Instructions, want)
		}
	}
}

func TestGetEmailTaskInstructionsUseReferenceSpokenSymbolGuidance(t *testing.T) {
	task := NewGetEmailTask(GetEmailOptions{})

	for _, want := range []string{
		"Convert words like 'dot', 'underscore', 'dash', 'plus' into symbols: `.`, `_`, `-`, `+`.",
		"Convert 'at' to `@`.",
	} {
		if !strings.Contains(task.Instructions, want) {
			t.Fatalf("Instructions = %q, want spoken symbol guidance %q", task.Instructions, want)
		}
	}
}

func TestGetEmailTaskInstructionsPreserveReferenceModalityVariants(t *testing.T) {
	task := NewGetEmailTask(GetEmailOptions{})

	if task.InstructionVariants == nil {
		t.Fatal("InstructionVariants = nil, want reference audio/text instruction variants")
	}
	audio := task.InstructionVariants.AsModality("audio").String()
	text := task.InstructionVariants.AsModality("text").String()

	for _, want := range []string{
		"Handle input as noisy voice transcription.",
		"john dot doe at gmail dot com",
		"Convert words like 'dot', 'underscore', 'dash', 'plus' into symbols: `.`, `_`, `-`, `+`.",
		"Call `confirm_email_address` after the user confirmed the email address is correct.",
	} {
		if !strings.Contains(audio, want) {
			t.Fatalf("audio instructions = %q, want reference audio guidance %q", audio, want)
		}
	}
	for _, want := range []string{
		"Handle input as typed text. Expect users to type their email address directly in standard format.",
		"If the address looks almost correct but has minor typos (e.g. missing '@' or domain), prompt for clarification.",
	} {
		if !strings.Contains(text, want) {
			t.Fatalf("text instructions = %q, want reference text guidance %q", text, want)
		}
	}
	for _, stale := range []string{
		"Handle input as noisy voice transcription.",
		"john dot doe at gmail dot com",
		"Call `confirm_email_address` after the user confirmed the email address is correct.",
	} {
		if strings.Contains(text, stale) {
			t.Fatalf("text instructions = %q, want no audio/default-confirmation guidance %q", text, stale)
		}
	}
}

func TestGetEmailTaskInstructionPartsCustomizePersonaAndExtra(t *testing.T) {
	customPersona := "You only collect work email addresses for account recovery."
	task := NewGetEmailTask(GetEmailOptions{
		Instructions: &beta.InstructionParts{
			Persona: &customPersona,
			Extra:   "Ask for the company domain when the user gives a personal address.",
		},
	})

	if !strings.Contains(task.Instructions, customPersona) {
		t.Fatalf("Instructions = %q, want custom persona", task.Instructions)
	}
	if strings.Contains(task.Instructions, "responsible solely for capturing an email address") {
		t.Fatalf("Instructions = %q, want default persona replaced", task.Instructions)
	}
	if !strings.Contains(task.Instructions, "Ask for the company domain when the user gives a personal address.") {
		t.Fatalf("Instructions = %q, want extra instructions appended", task.Instructions)
	}
}

func TestGetEmailTaskAppendsReferenceExtraInstructions(t *testing.T) {
	opts := GetEmailOptions{}
	field := reflect.ValueOf(&opts).Elem().FieldByName("ExtraInstructions")
	if !field.IsValid() {
		t.Fatal("GetEmailOptions.ExtraInstructions missing; want reference extra_instructions constructor option")
	}
	field.SetString("Ask whether this is the user's work or personal email.")

	task := NewGetEmailTask(opts)

	if !strings.Contains(task.Instructions, "Ask whether this is the user's work or personal email.") {
		t.Fatalf("Instructions = %q, want extra instructions appended", task.Instructions)
	}
	text := task.InstructionVariants.AsModality("text").String()
	if !strings.Contains(text, "Ask whether this is the user's work or personal email.") {
		t.Fatalf("text instructions = %q, want extra instructions appended", text)
	}
}

func TestGetEmailTaskIgnoresExtraInstructionsWithCustomInstructions(t *testing.T) {
	persona := "You only collect work emails."
	task := NewGetEmailTask(GetEmailOptions{
		Instructions:      &beta.InstructionParts{Persona: &persona, Extra: "Only ask for work-email details."},
		ExtraInstructions: "Ask whether this is personal or work email.",
	})

	if strings.Contains(task.Instructions, "Ask whether this is personal or work email.") {
		t.Fatalf("Instructions = %q, want ExtraInstructions ignored when custom Instructions are provided", task.Instructions)
	}
	text := task.InstructionVariants.AsModality("text").String()
	if strings.Contains(text, "Ask whether this is personal or work email.") {
		t.Fatalf("text instructions = %q, want ExtraInstructions ignored when custom Instructions are provided", text)
	}
	if !strings.Contains(task.Instructions, "Only ask for work-email details.") {
		t.Fatalf("Instructions = %q, want custom InstructionParts extra preserved", task.Instructions)
	}
}

func TestGetEmailTaskInstructionPartsCanRemovePersona(t *testing.T) {
	emptyPersona := ""
	task := NewGetEmailTask(GetEmailOptions{
		Instructions: &beta.InstructionParts{Persona: &emptyPersona},
	})

	if strings.Contains(task.Instructions, "responsible solely for capturing an email address") {
		t.Fatalf("Instructions = %q, want default persona removed", task.Instructions)
	}
	if !strings.Contains(task.Instructions, "Call `update_email_address` at the first opportunity") {
		t.Fatalf("Instructions = %q, want workflow guidance preserved", task.Instructions)
	}
}

func TestGetEmailTaskOnEnterUsesReferencePrompt(t *testing.T) {
	want := "Ask the user to provide an email address."
	if got := emailOnEnterPrompt(); got != want {
		t.Fatalf("emailOnEnterPrompt() = %q, want %q", got, want)
	}

	task := NewGetEmailTask(GetEmailOptions{})
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
		if ev.SpeechHandle == nil {
			t.Fatal("SpeechCreated SpeechHandle = nil, want on-enter reply handle")
		}
		if ev.SpeechHandle.Generation.UserMessage != nil {
			t.Fatalf("on-enter UserMessage = %#v, want nil for instruction-backed prompt", ev.SpeechHandle.Generation.UserMessage)
		}
		if ev.SpeechHandle.Generation.Instructions == nil {
			t.Fatal("on-enter instructions = nil, want reference prompt")
		}
		if got := ev.SpeechHandle.Generation.Instructions.AsModality("text").String(); got != want {
			t.Fatalf("on-enter instructions = %q, want %q", got, want)
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for email on-enter prompt")
	}
}

func TestGetEmailTaskStaleConfirmationPromptsForUpdatedEmail(t *testing.T) {
	task := NewGetEmailTask(GetEmailOptions{})
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
		t.Fatal("timed out waiting for email on-enter prompt")
	}

	update := &updateEmailTool{task: task}

	if _, err := update.Execute(context.Background(), `{"email":"ada@example.com"}`); err != nil {
		t.Fatalf("first update Execute() error = %v", err)
	}
	staleConfirm := &confirmEmailTool{task: task, email: "ada@example.com"}

	if _, err := update.Execute(context.Background(), `{"email":"grace@example.com"}`); err != nil {
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
		want := emailStaleConfirmationPrompt()
		if ev.SpeechHandle.Generation.Instructions == nil {
			t.Fatal("stale confirmation instructions = nil, want changed-email prompt")
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

func TestDeclineEmailCaptureToolFailsWithReason(t *testing.T) {
	task := NewGetEmailTask(GetEmailOptions{RequireConfirmationSet: true})
	tool := &declineEmailCaptureTool{task: task}

	out, err := tool.Execute(context.Background(), `{"reason":"user refused"}`)
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
	want := "couldn't get the email address: user refused"
	if toolErr.Message != want {
		t.Fatalf("ToolError.Message = %q, want %q", toolErr.Message, want)
	}
}

func TestDeclineEmailCaptureToolUsesRunContextCurrentAgent(t *testing.T) {
	staleTask := NewGetEmailTask(GetEmailOptions{})
	currentTask := NewGetEmailTask(GetEmailOptions{})
	session := agent.NewAgentSession(currentTask, nil, agent.AgentSessionOptions{})
	ctx := agent.WithRunContext(context.Background(), agent.NewRunContext(session, nil, nil))
	tool := &declineEmailCaptureTool{task: staleTask}

	out, err := tool.Execute(ctx, `{"reason":"user refused current email"}`)
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
		want := "couldn't get the email address: user refused current email"
		if toolErr.Message != want {
			t.Fatalf("ToolError.Message = %q, want %q", toolErr.Message, want)
		}
	case <-time.After(time.Second):
		t.Fatal("current task did not fail after decline_email_capture")
	}

	select {
	case err := <-staleTask.Err:
		t.Fatalf("stale task failed with %v, want decline routed to current agent", err)
	default:
	}
}

func TestDeclineEmailCaptureToolUsesReferenceSchema(t *testing.T) {
	tool := &declineEmailCaptureTool{task: NewGetEmailTask(GetEmailOptions{})}

	wantDescription := "Handles the case when the user explicitly declines to provide an email address."
	if got := tool.Description(); got != wantDescription {
		t.Fatalf("decline_email_capture description = %q, want %q", got, wantDescription)
	}
	properties, ok := tool.Parameters()["properties"].(map[string]any)
	if !ok {
		t.Fatalf("properties = %#v, want map", tool.Parameters()["properties"])
	}
	reason, ok := properties["reason"].(map[string]any)
	if !ok {
		t.Fatalf("reason schema = %#v, want map", properties["reason"])
	}
	wantParam := "A short explanation of why the user declined to provide the email address"
	if got := reason["description"]; got != wantParam {
		t.Fatalf("reason description = %#v, want %q", got, wantParam)
	}
}

type referenceEmailExtraTool struct {
	id string
}

func (t referenceEmailExtraTool) ID() string          { return t.id }
func (t referenceEmailExtraTool) Name() string        { return t.id }
func (t referenceEmailExtraTool) Description() string { return "reference extra email tool" }
func (t referenceEmailExtraTool) Parameters() map[string]any {
	return map[string]any{"type": "object", "properties": map[string]any{}}
}
func (t referenceEmailExtraTool) Execute(ctx context.Context, args string) (string, error) {
	return "", nil
}
