// Copyright 2020 Thomas.Hoehenleitner [at] seerose.net
// Use of this source code is governed by a license that can be found in the LICENSE file.

// Package decoder provides several decoders for differently encoded trice streams.
package decoder

import (
	"encoding/binary"
	"fmt"
	"io"
	"regexp"
	"strings"
	"sync"

	"github.com/rokath/trice/internal/id"
)

const (
	// defaultSize is the beginning receive and sync buffer size.
	DefaultSize = 64 * 1014

	// patNextFormatSpecifier is a regex to find next format specifier in a string (exclude %%*) and ignoring %s
	//
	// https://regex101.com/r/BjiD5M/1
	// Language C plus from language Go: %b, %F, %q
	// Partial implemented: %hi, %hu, %ld, %li, %lf, %Lf, %Lu, %lli, %lld
	// Not implemented: %s
	//patNextFormatSpecifier = `(?:^|[^%])(%[0-9]*(-|c|d|e|E|f|F|g|G|h|i|l|L|o|O|p|q|u|x|X|n|b))`
	//patNextFormatSpecifier = `%([+\-#'0-9\.0-9])*(c|d|e|E|f|F|g|G|h|i|l|L|o|O|p|q|u|x|X|n|b|t)` // assumes no `%%` inside string!
	patNextFormatSpecifier = `%([+\-#'0-9\.0-9])*(b|c|d|e|f|g|E|F|G|h|i|l|L|n|o|O|p|q|t|u|x|X)` // assumes no `%%` inside string!

	// patNextFormatUSpecifier is a regex to find next format u specifier in a string
	// It does also match %%u positions!
	//patNextFormatUSpecifier = `(?:%[0-9]*u)`
	patNextFormatUSpecifier = `%[0-9]*u` // assumes no `%%` inside string!

	// patNextFormatISpecifier is a regex to find next format i specifier in a string
	// It does also match %%i positions!
	patNextFormatISpecifier = `%[0-9]*i` // assumes no `%%` inside string!

	// patNextFormatXSpecifier is a regex to find next format x specifier in a string
	// It does also match %%x positions!
	// patNextFormatXSpecifier = `(?:%[0-9]*(l|o|O|x|X|b))`
	patNextFormatXSpecifier = `%[0-9]*(l|o|O|x|X|b|p|t)` // assumes no `%%` inside string!

	// patNextFormatFSpecifier is a regex to find next format f specifier in a string
	// It does also match %%f positions!
	patNextFormatFSpecifier = `%[(+\-0-9\.0-9#]*(e|E|f|F|g|G)` // assumes no `%%` inside string!

	// patNextFormatBoolSpecifier is a regex to find next format f specifier in a string
	// It does also match %%t positions!
	patNextFormatBoolSpecifier = `%t` // assumes no `%%` inside string!

	// patNextFormatPointerSpecifier is a regex to find next format f specifier in a string
	// It does also match %%t positions!
	patNextFormatPointerSpecifier = `%p` // assumes no `%%` inside string!

	// hints is the help information in case of errors.
	Hints = "att:Hints:Baudrate? Encoding? Interrupt? Overflow? Parameter count? Password? til.json? Version?"
)

var (
	// Verbose gives more information on output if set. The value is injected from main packages.
	Verbose bool

	// ShowID is used as format string for displaying the first trice ID at the start of each line if not "".
	ShowID string

	// decoder.LastTriceID is last decoded ID. It is used for switch -showID.
	LastTriceID id.TriceID

	// TestTableMode is a special option for easy decoder test table generation.
	TestTableMode bool

	// Unsigned if true, forces hex and in values printed as unsigned values.
	Unsigned bool

	matchNextFormatSpecifier        = regexp.MustCompile(patNextFormatSpecifier)
	matchNextFormatUSpecifier       = regexp.MustCompile(patNextFormatUSpecifier)
	matchNextFormatISpecifier       = regexp.MustCompile(patNextFormatISpecifier)
	matchNextFormatXSpecifier       = regexp.MustCompile(patNextFormatXSpecifier)
	matchNextFormatFSpecifier       = regexp.MustCompile(patNextFormatFSpecifier)
	matchNextFormatBoolSpecifier    = regexp.MustCompile(patNextFormatBoolSpecifier)
	matchNextFormatPointerSpecifier = regexp.MustCompile(patNextFormatPointerSpecifier)

	DebugOut                        = false // DebugOut enables debug information.
	DumpLineByteCount               int     // DumpLineByteCount is the bytes per line for the dumpDec decoder.
	InitialCycle                    = true  // InitialCycle is a helper for the cycle counter automatic.
	TargetTimestamp                 uint32  // targetTimestamp contains target specific timestamp value.
	TargetLocation                  uint32  // targetLocation contains 16 bit file id in high and 16 bit line number in low part.
	ShowTargetTimestamp             string  // ShowTargetTimestamp is the format string for target timestamps.
	LocationInformationFormatString string  // LocationInformationFormatString is the format string for target location: line number and file name.
	TargetTimestampExists           bool    // targetTimestampExists is set in dependence of p.COBSModeDescriptor.
	TargetLocationExists            bool    // targetLocationExists is set in dependence of p.COBSModeDescriptor.
)

// newDecoder abstracts the function type for a new decoder.
// stype newDecoder func(out io.Writer, lut id.TriceIDLookUp, m *sync.RWMutex, li id.TriceIDLookUpLI, in io.Reader, endian bool) Decoder

// Decoder is providing a byte reader returning decoded trice's.
// setInput allows switching the input stream to a different source.
type Decoder interface {
	io.Reader
	setInput(io.Reader)
}

// DecoderData is the common data struct for all decoders.
type DecoderData struct {
	W          io.Writer          // io.Stdout or the like
	In         io.Reader          // in is the inner reader, which is used to get raw bytes
	IBuf       []byte             // iBuf holds unprocessed (raw) bytes for interpretation.
	B          []byte             // read buffer holds a single decoded COBS package, which can contain several trices.
	Endian     bool               // endian is true for LittleEndian and false for BigEndian
	TriceSize  int                // trice head and payload size as number of bytes
	ParamSpace int                // trice payload size after head
	SLen       int                // string length for TRICE_S
	Lut        id.TriceIDLookUp   // id look-up map for translation
	LutMutex   *sync.RWMutex      // to avoid concurrent map read and map write during map refresh triggered by filewatcher
	Li         id.TriceIDLookUpLI // location information map
	Trice      id.TriceFmt        // id.TriceFmt // received trice
	//lastInnerRead     time.Time
	//innerReadInterval time.Duration
}

// setInput allows switching the input stream to a different source.
//
// This function is for easier testing with cycle counters.
func (p *DecoderData) setInput(r io.Reader) {
	p.In = r
}

// ReadU16 returns the 2 b bytes as uint16 according the specified endianness
func (p *DecoderData) ReadU16(b []byte) uint16 {
	if p.Endian {
		return binary.LittleEndian.Uint16(b)
	}
	return binary.BigEndian.Uint16(b)
}

// ReadU32 returns the 4 b bytes as uint32 according the specified endianness
func (p *DecoderData) ReadU32(b []byte) uint32 {
	if p.Endian {
		return binary.LittleEndian.Uint32(b)
	}
	return binary.BigEndian.Uint32(b)
}

// ReadU64 returns the 8 b bytes as uint64 according the specified endianness
func (p *DecoderData) ReadU64(b []byte) uint64 {
	if p.Endian {
		return binary.LittleEndian.Uint64(b)
	}
	return binary.BigEndian.Uint64(b)
}

// UReplaceN checks all format specifier in i and replaces %nu with %nd and returns that result as o.
//
// If a replacement took place on position k u[k] is 1. Afterwards len(u) is amount of found format specifiers.
// Additional, if UnsignedHex is true, for FormatX specifiers u[k] is also 1.
// If a float format specifier was found at position k, u[k] is 2,
// http://www.cplusplus.com/reference/cstdio/printf/
// https://www.codingunit.com/printf-format-specifiers-format-conversions-and-formatted-output
func UReplaceN(i string) (o string, u []int) {
	o = i
	i = strings.ReplaceAll(i, "%%", "__") // this makes regex easier and faster
	var offset int
	for {
		s := i[offset:] // remove processed part
		loc := matchNextFormatSpecifier.FindStringIndex(s)
		if nil == loc { // no (more) fm found
			return
		}
		offset += loc[1] // track position
		fm := s[loc[0]:loc[1]]
		locPointer := matchNextFormatPointerSpecifier.FindStringIndex(fm)
		if nil != locPointer { // a %p found
			u = append(u, 4) // pointer value
			continue
		}
		locBool := matchNextFormatBoolSpecifier.FindStringIndex(fm)
		if nil != locBool { // a %t found
			u = append(u, 3) // bool value
			continue
		}
		locF := matchNextFormatFSpecifier.FindStringIndex(fm)
		if nil != locF { // a %nf found
			u = append(u, 2) // float value
			continue
		}
		locU := matchNextFormatUSpecifier.FindStringIndex(fm)
		if nil != locU { // a %nu found
			o = o[:offset-1] + "d" + o[offset:] // replace %nu -> %nd
			u = append(u, 0)                    // no negative values
			continue
		}
		locI := matchNextFormatISpecifier.FindStringIndex(fm)
		if nil != locI { // a %ni found
			o = o[:offset-1] + "d" + o[offset:] // replace %ni -> %nd
			u = append(u, 1)                    // also negative values
			continue
		}
		locX := matchNextFormatXSpecifier.FindStringIndex(fm)
		if nil != locX { // a %nx, %nX or, %no, %nO or %nb found
			if Unsigned {
				u = append(u, 0) // no negative values
			} else {
				u = append(u, 1) // also negative values
			}
			continue
		}
		u = append(u, 1) // keep sign in all other cases(also negative values)
	}
}

// Dump prints the byte slice as hex in one line
func Dump(w io.Writer, b []byte) {
	for _, x := range b {
		fmt.Fprintf(w, "%02x ", x)
	}
	fmt.Fprintln(w, "")
}
