package main

/*
#cgo pkg-config: opus
#include <opus.h>
*/
import "C"
import "fmt"

func main() {
	size := C.opus_decoder_get_size(C.int(1))
	fmt.Printf("Opus decoder size for 1 channel: %d\n", size)
	
	size2 := C.opus_decoder_get_size(C.int(2))
	fmt.Printf("Opus decoder size for 2 channels: %d\n", size2)
	
	size0 := C.opus_decoder_get_size(C.int(0))
	fmt.Printf("Opus decoder size for 0 channels: %d\n", size0)
}
