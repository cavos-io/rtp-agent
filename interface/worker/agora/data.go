package agora

import "context"

type DataPublisher interface {
	PublishData(context.Context, []byte) error
	Close(context.Context) error
}
