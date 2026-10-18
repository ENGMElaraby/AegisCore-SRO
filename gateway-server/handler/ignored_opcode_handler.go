package handler

import (
	"context"
	"fmt"
	"strconv"
	"strings"

	"github.com/Harvi8/AegisCore/framework/network"
	"github.com/Harvi8/AegisCore/framework/server"
	log "github.com/sirupsen/logrus"
)

// coreOpcodes are handled by the protocol state machine and can never be ignored.
var coreOpcodes = map[uint16]bool{0x5000: true, 0x9000: true, 0x2001: true, 0x2002: true}

// ParseIgnoredOpcodes reads GATEWAY_IGNORED_OPCODES: a comma-separated list of hex opcodes (with or without 0x) that
// third-party client add-ons send, e.g. MaxiGuard's 0xA175. Blank means none.
func ParseIgnoredOpcodes(value string) ([]uint16, error) {
	if strings.TrimSpace(value) == "" {
		return nil, nil
	}
	parts := strings.Split(value, ",")
	opcodes := make([]uint16, 0, len(parts))
	for _, part := range parts {
		text := strings.TrimSpace(part)
		text = strings.TrimPrefix(strings.TrimPrefix(text, "0x"), "0X")
		parsed, err := strconv.ParseUint(text, 16, 16)
		if err != nil {
			return nil, fmt.Errorf("ignored opcode %q is not a 16-bit hex value", strings.TrimSpace(part))
		}
		if coreOpcodes[uint16(parsed)] {
			return nil, fmt.Errorf("core opcode 0x%04X cannot be ignored", parsed)
		}
		opcodes = append(opcodes, uint16(parsed))
	}
	return opcodes, nil
}

// IgnoreOpcode accepts and drops a packet without replying.
func IgnoreOpcode(_ context.Context, _ *server.Session, packet network.Packet) error {
	log.Debugf("ignoring client opcode 0x%04X (%d bytes)", packet.MessageID, packet.PayloadLen())
	return nil
}

// RegisterIgnoredOpcodeHandlers drops each listed opcode. The caller must also admit them on the listener.
func RegisterIgnoredOpcodeHandlers(target *server.Server, opcodes []uint16) {
	if target == nil {
		return
	}
	for _, opcode := range opcodes {
		target.RegisterHandler(opcode, IgnoreOpcode)
	}
}
