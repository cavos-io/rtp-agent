package agora

import "errors"

type rtmLifecycleClient interface {
	Unsubscribe(string) error
	Logout() error
	Release()
}

func closeRTMClient(client rtmLifecycleClient, channel string) error {
	if client == nil {
		return nil
	}
	var errs []error
	if err := client.Unsubscribe(channel); err != nil {
		errs = append(errs, err)
	}
	if err := client.Logout(); err != nil {
		errs = append(errs, err)
	}
	client.Release()
	return errors.Join(errs...)
}
