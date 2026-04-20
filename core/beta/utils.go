package beta

import (
	"fmt"
	"strings"
)

type DtmfEvent string

const (
	DtmfEventOne   DtmfEvent = "1"
	DtmfEventTwo   DtmfEvent = "2"
	DtmfEventThree DtmfEvent = "3"
	DtmfEventFour  DtmfEvent = "4"
	DtmfEventFive  DtmfEvent = "5"
	DtmfEventSix   DtmfEvent = "6"
	DtmfEventSeven DtmfEvent = "7"
	DtmfEventEight DtmfEvent = "8"
	DtmfEventNine  DtmfEvent = "9"
	DtmfEventZero  DtmfEvent = "0"
	DtmfEventStar  DtmfEvent = "*"
	DtmfEventPound DtmfEvent = "#"
	DtmfEventA     DtmfEvent = "A"
	DtmfEventB     DtmfEvent = "B"
	DtmfEventC     DtmfEvent = "C"
	DtmfEventD     DtmfEvent = "D"
)

func FormatDtmf(events []DtmfEvent) string {
	var vals []string
	for _, e := range events {
		vals = append(vals, string(e))
	}
	return strings.Join(vals, " ")
}

func DtmfEventToCode(event DtmfEvent) (int, error) {
	switch event {
	case DtmfEventStar:
		return 10, nil
	case DtmfEventPound:
		return 11, nil
	case DtmfEventA:
		return 12, nil
	case DtmfEventB:
		return 13, nil
	case DtmfEventC:
		return 14, nil
	case DtmfEventD:
		return 15, nil
	default:
		if len(event) > 0 && event[0] >= '0' && event[0] <= '9' {
			return int(event[0] - '0'), nil
		}
		return 0, fmt.Errorf("invalid DTMF event: %s", event)
	}
}

