package handler

import (
	"context"
	"reflect"
	"testing"

	"github.com/Harvi8/AegisCore/framework/network"
)

func TestParseIgnoredOpcodesAcceptsHexList(t *testing.T) {
	got, err := ParseIgnoredOpcodes(" 0xA175, a176 ,0XA177")

	if err != nil {
		t.Fatalf("ParseIgnoredOpcodes() error = %v", err)
	}
	if want := []uint16{0xA175, 0xA176, 0xA177}; !reflect.DeepEqual(got, want) {
		t.Fatalf("ParseIgnoredOpcodes() = %#v, want %#v", got, want)
	}
}

func TestParseIgnoredOpcodesEmptyMeansNone(t *testing.T) {
	got, err := ParseIgnoredOpcodes("  ")

	if err != nil || len(got) != 0 {
		t.Fatalf("ParseIgnoredOpcodes(blank) = %v, %v; want none", got, err)
	}
}

func TestParseIgnoredOpcodesRejectsInvalidAndCoreOpcodes(t *testing.T) {
	for _, input := range []string{"0xZZZZ", "0x1FFFF", "0x5000", "0x2001", "0xA175,,"} {
		if _, err := ParseIgnoredOpcodes(input); err == nil {
			t.Fatalf("ParseIgnoredOpcodes(%q) = nil error, want an error", input)
		}
	}
}

func TestIgnoreOpcodeDropsPacketWithoutError(t *testing.T) {
	if err := IgnoreOpcode(context.Background(), nil, network.Packet{}); err != nil {
		t.Fatalf("IgnoreOpcode() error = %v", err)
	}
}
