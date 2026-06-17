//go:build !agora_sdk

package agora

import "fmt"

func NewSDKChannelClient() (ChannelClient, error) {
	return nil, fmt.Errorf("agora SDK channel client requires the agora_sdk build tag and Agora native runtime libraries")
}

func NewSDKDataPublisher(Options) (DataPublisher, error) {
	return nil, fmt.Errorf("agora SDK data publisher requires the agora_sdk build tag and Agora native runtime libraries")
}
