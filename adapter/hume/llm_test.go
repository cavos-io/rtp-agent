package hume

import (
	"errors"
	"io"
	"net/http"
	"testing"
)

type humeLLMCloseErrorBody struct {
	closed bool
}

func (b *humeLLMCloseErrorBody) Read(_ []byte) (int, error) {
	if b.closed {
		return 0, errors.New("read after close")
	}
	return 0, io.EOF
}

func (b *humeLLMCloseErrorBody) Close() error {
	b.closed = true
	return nil
}

func TestHumeLLMStreamNextAfterCloseReturnsEOF(t *testing.T) {
	body := &humeLLMCloseErrorBody{}
	stream := &humeLLMStream{resp: &http.Response{Body: body}}

	if err := stream.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
	}

	_, err := stream.Next()

	if !errors.Is(err, io.EOF) {
		t.Fatalf("Next() error = %v, want io.EOF", err)
	}
}
