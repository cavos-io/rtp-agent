package workflows

import (
	"strings"

	beta "github.com/cavos-io/rtp-agent/core/beta"
)

func applyInstructionParts(instructions, defaultPersona string, parts *beta.InstructionParts) string {
	if parts == nil {
		return instructions
	}

	persona := defaultPersona
	if parts.Persona != nil {
		persona = *parts.Persona
	}
	if strings.HasPrefix(instructions, defaultPersona) {
		instructions = persona + strings.TrimPrefix(instructions, defaultPersona)
	} else if persona != "" {
		instructions = persona + "\n" + instructions
	}

	instructions = strings.TrimLeft(instructions, "\n")
	if extra := strings.TrimSpace(parts.Extra); extra != "" {
		instructions = strings.TrimRight(instructions, "\n") + "\n" + extra
	}
	return instructions
}
