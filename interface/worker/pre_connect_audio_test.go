package worker

import (
	"reflect"
	"testing"
	lksdk "github.com/livekit/server-sdk-go/v2"
)

func TestInfo(t *testing.T) {
	var info lksdk.ByteStreamInfo
	val := reflect.ValueOf(info)
	typ := val.Type().Field(0).Type.Elem()
	for i := 0; i < typ.NumField(); i++ {
		t.Logf("Embedded Field %d: %s", i, typ.Field(i).Name)
	}
}

