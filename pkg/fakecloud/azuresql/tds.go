// Copyright 2026 High Five Labs
// SPDX-License-Identifier: MIT

package azuresql

// The wire protocol: TDS 7.4 as a server speaks it, from the protocol's
// documentation (https://learn.microsoft.com/openspecs/windows_protocols/ms-tds/)
// and only as much as go-mssqldb uses: PRELOGIN, the TLS handshake carried
// in PRELOGIN packets, LOGIN7 with a SQL password or a federated
// authentication security token, SQL batches, RPC calls to sp_executesql
// with nvarchar parameters, and the ERROR, LOGINACK, FEATUREEXTACK,
// COLMETADATA, ROW, RETURNSTATUS and DONE tokens back.

import (
	"bytes"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"unicode/utf16"
)

// Packet types.
const (
	packSQLBatch  = 0x01
	packRPC       = 0x03
	packReply     = 0x04
	packAttention = 0x06
	packLogin7    = 0x10
	packPrelogin  = 0x12
)

// Tokens.
const (
	tokReturnStatus  = 0x79
	tokColMetadata   = 0x81
	tokError         = 0xAA
	tokLoginAck      = 0xAD
	tokFeatureExtAck = 0xAE
	tokRow           = 0xD1
	tokEnvChange     = 0xE3
	tokDone          = 0xFD
	tokDoneProc      = 0xFE
	tokDoneInProc    = 0xFF
)

// DONE status bits.
const (
	doneMore  = 0x01
	doneError = 0x02
	doneCount = 0x10
	doneAttn  = 0x20
)

// Type identifiers.
const (
	typeIntN     = 0x26
	typeNVarChar = 0xE7
)

const (
	headerSize = 8
	// packetSize is what this server writes: go-mssqldb's default, which a
	// client never lowers.
	packetSize = 4096

	preloginVersion    = 0
	preloginEncryption = 1
	preloginInstOpt    = 2
	preloginThreadID   = 3
	preloginMARS       = 4
	preloginFedAuth    = 6
	preloginTerminator = 0xFF
	encryptRequired    = 3

	featureFedAuth        = 0x02
	featureTerminator     = 0xFF
	fedAuthSecurityToken  = 0x01
	loginOptionExtension  = 0x10
	tdsVersion74          = 0x74000004
	procIDSpExecuteSQL    = 10
	fixedCollationLatin1  = "\x09\x04\xd0\x00\x34"
	plpNull               = 0xFFFFFFFFFFFFFFFF
	shortLenMax           = 0xFFFF
	loginFixedHeaderBytes = 94
)

// readMessage reads packets until the end-of-message bit and returns their
// type and joined payload.
func readMessage(r io.Reader) (byte, []byte, error) {
	var payload []byte
	var typ byte
	for {
		var h [headerSize]byte
		if _, err := io.ReadFull(r, h[:]); err != nil {
			return 0, nil, err
		}
		size := int(binary.BigEndian.Uint16(h[2:4]))
		if size < headerSize {
			return 0, nil, fmt.Errorf("packet size %d shorter than its header", size)
		}
		body := make([]byte, size-headerSize)
		if _, err := io.ReadFull(r, body); err != nil {
			return 0, nil, err
		}
		typ = h[0]
		payload = append(payload, body...)
		if h[1]&0x01 != 0 {
			return typ, payload, nil
		}
	}
}

// writeMessage writes payload as packets of typ, the last marked
// end-of-message.
func writeMessage(w io.Writer, typ byte, payload []byte) error {
	var out bytes.Buffer
	id := byte(1)
	for {
		n := len(payload)
		if n > packetSize-headerSize {
			n = packetSize - headerSize
		}
		status := byte(0)
		if n == len(payload) {
			status = 0x01
		}
		h := [headerSize]byte{typ, status, 0, 0, 0, 0, id, 0}
		binary.BigEndian.PutUint16(h[2:4], uint16(headerSize+n))
		out.Write(h[:])
		out.Write(payload[:n])
		payload = payload[n:]
		id++
		if len(payload) == 0 {
			break
		}
	}
	_, err := w.Write(out.Bytes())
	return err
}

// handshakeConn carries the TLS handshake inside PRELOGIN packets, as TDS
// before 8.0 does; once the handshake is done it passes bytes through.
type handshakeConn struct {
	rw       io.ReadWriter
	pending  []byte
	finished bool
}

func (c *handshakeConn) Read(b []byte) (int, error) {
	if c.finished {
		return c.rw.Read(b)
	}
	for len(c.pending) == 0 {
		var h [headerSize]byte
		if _, err := io.ReadFull(c.rw, h[:]); err != nil {
			return 0, err
		}
		if h[0] != packPrelogin {
			return 0, fmt.Errorf("packet type %#x during the TLS handshake, want PRELOGIN", h[0])
		}
		size := int(binary.BigEndian.Uint16(h[2:4]))
		if size < headerSize {
			return 0, errors.New("short packet during the TLS handshake")
		}
		c.pending = make([]byte, size-headerSize)
		if _, err := io.ReadFull(c.rw, c.pending); err != nil {
			return 0, err
		}
	}
	n := copy(b, c.pending)
	c.pending = c.pending[n:]
	return n, nil
}

func (c *handshakeConn) Write(b []byte) (int, error) {
	if c.finished {
		return c.rw.Write(b)
	}
	if err := writeMessage(c.rw, packPrelogin, b); err != nil {
		return 0, err
	}
	return len(b), nil
}

// parsePrelogin reads a PRELOGIN payload's options.
func parsePrelogin(p []byte) (map[byte][]byte, error) {
	fields := map[byte][]byte{}
	for i := 0; ; i += 5 {
		if i >= len(p) {
			return nil, errors.New("PRELOGIN without a terminator")
		}
		if p[i] == preloginTerminator {
			return fields, nil
		}
		if i+5 > len(p) {
			return nil, errors.New("short PRELOGIN option")
		}
		off := int(binary.BigEndian.Uint16(p[i+1:]))
		n := int(binary.BigEndian.Uint16(p[i+3:]))
		if off+n > len(p) {
			return nil, errors.New("PRELOGIN option past the end")
		}
		fields[p[i]] = p[off : off+n]
	}
}

// preloginReply is the server's PRELOGIN: encryption required, and the
// federated authentication flag echoed when the client sent it.
func preloginReply(clientFields map[byte][]byte) []byte {
	type opt struct {
		token byte
		data  []byte
	}
	opts := []opt{
		{preloginVersion, []byte{12, 0, 0x07, 0xD0, 0, 0}},
		{preloginEncryption, []byte{encryptRequired}},
		{preloginInstOpt, []byte{0}},
		{preloginThreadID, nil},
		{preloginMARS, []byte{0}},
	}
	if _, ok := clientFields[preloginFedAuth]; ok {
		opts = append(opts, opt{preloginFedAuth, []byte{1}})
	}
	var head, data bytes.Buffer
	off := len(opts)*5 + 1
	for _, o := range opts {
		head.WriteByte(o.token)
		_ = binary.Write(&head, binary.BigEndian, uint16(off+data.Len()))
		_ = binary.Write(&head, binary.BigEndian, uint16(len(o.data)))
		data.Write(o.data)
	}
	head.WriteByte(preloginTerminator)
	return append(head.Bytes(), data.Bytes()...)
}

// login is what a LOGIN7 says.
type login struct {
	user, password, database string
	token                    string
	hasToken                 bool
}

func ucs2(b []byte) string {
	u := make([]uint16, len(b)/2)
	for i := range u {
		u[i] = binary.LittleEndian.Uint16(b[2*i:])
	}
	return string(utf16.Decode(u))
}

func toUCS2(s string) []byte {
	u := utf16.Encode([]rune(s))
	b := make([]byte, 2*len(u))
	for i, c := range u {
		binary.LittleEndian.PutUint16(b[2*i:], c)
	}
	return b
}

// parseLogin7 reads the parts of a LOGIN7 this server uses.
func parseLogin7(p []byte) (login, error) {
	var l login
	if len(p) < loginFixedHeaderBytes {
		return l, errors.New("short LOGIN7")
	}
	field := func(at int) (string, error) {
		off := int(binary.LittleEndian.Uint16(p[at:]))
		n := int(binary.LittleEndian.Uint16(p[at+2:])) * 2
		if off+n > len(p) {
			return "", errors.New("LOGIN7 field past the end")
		}
		return ucs2(p[off : off+n]), nil
	}
	var err error
	if l.user, err = field(40); err != nil {
		return l, err
	}
	pwOff := int(binary.LittleEndian.Uint16(p[44:]))
	pwLen := int(binary.LittleEndian.Uint16(p[46:])) * 2
	if pwOff+pwLen > len(p) {
		return l, errors.New("LOGIN7 password past the end")
	}
	pw := make([]byte, pwLen)
	for i, b := range p[pwOff : pwOff+pwLen] {
		b ^= 0xA5
		pw[i] = b<<4 | b>>4
	}
	l.password = ucs2(pw)
	if l.database, err = field(68); err != nil {
		return l, err
	}
	optionFlags3 := p[27]
	if optionFlags3&loginOptionExtension == 0 {
		return l, nil
	}
	extOff := int(binary.LittleEndian.Uint16(p[56:]))
	if extOff+4 > len(p) {
		return l, errors.New("LOGIN7 extension past the end")
	}
	at := int(binary.LittleEndian.Uint32(p[extOff:]))
	for at < len(p) && p[at] != featureTerminator {
		if at+5 > len(p) {
			return l, errors.New("short LOGIN7 feature")
		}
		id := p[at]
		n := int(binary.LittleEndian.Uint32(p[at+1:]))
		data := p[at+5 : min(at+5+n, len(p))]
		if id == featureFedAuth && len(data) >= 5 && data[0]>>1 == fedAuthSecurityToken {
			tn := int(binary.LittleEndian.Uint32(data[1:]))
			if 5+tn > len(data) {
				return l, errors.New("federated authentication token past the end")
			}
			l.token = ucs2(data[5 : 5+tn])
			l.hasToken = true
		}
		at += 5 + n
	}
	return l, nil
}

// tokens builds a reply's token stream.
type tokens struct{ bytes.Buffer }

func (t *tokens) u8(v byte)    { t.WriteByte(v) }
func (t *tokens) u16(v uint16) { _ = binary.Write(t, binary.LittleEndian, v) }
func (t *tokens) u32(v uint32) { _ = binary.Write(t, binary.LittleEndian, v) }
func (t *tokens) u64(v uint64) { _ = binary.Write(t, binary.LittleEndian, v) }
func (t *tokens) bVarChar(s string) {
	b := toUCS2(s)
	t.u8(byte(len(b) / 2))
	t.Write(b)
}

func (t *tokens) usVarChar(s string) {
	b := toUCS2(s)
	t.u16(uint16(len(b) / 2))
	t.Write(b)
}

// sqlError is an error the server reports to the client, with SQL Server's
// number for it.
type sqlError struct {
	Number int32
	Class  byte
	Msg    string
}

func (e *sqlError) Error() string { return fmt.Sprintf("mssql: %s (%d)", e.Msg, e.Number) }

func (t *tokens) errorToken(e *sqlError, server string) {
	var b tokens
	b.u32(uint32(e.Number))
	b.u8(1) // state
	b.u8(e.Class)
	b.usVarChar(e.Msg)
	b.bVarChar(server)
	b.bVarChar("")
	b.u32(1)
	t.u8(tokError)
	t.u16(uint16(b.Len()))
	t.Write(b.Bytes())
}

func (t *tokens) done(tok byte, status uint16, count uint64) {
	t.u8(tok)
	t.u16(status)
	t.u16(0xC1) // current command: SELECT; clients do not read it
	t.u64(count)
}

func (t *tokens) loginAck() {
	var b tokens
	b.u8(1) // interface: T-SQL
	_ = binary.Write(&b, binary.BigEndian, uint32(tdsVersion74))
	b.bVarChar("Microsoft SQL Server")
	b.Write([]byte{12, 0, 0x07, 0xD0})
	t.u8(tokLoginAck)
	t.u16(uint16(b.Len()))
	t.Write(b.Bytes())
}

func (t *tokens) databaseChange(name string) {
	var b tokens
	b.u8(1) // database
	b.bVarChar(name)
	b.bVarChar("master")
	t.u8(tokEnvChange)
	t.u16(uint16(b.Len()))
	t.Write(b.Bytes())
}

func (t *tokens) fedAuthAck() {
	t.u8(tokFeatureExtAck)
	t.u8(featureFedAuth)
	t.u32(0)
	t.u8(featureTerminator)
}

// resultSet writes one result: its columns (nvarchar, or int for a column
// named "") and rows.
func (t *tokens) resultSet(r *result) {
	t.u8(tokColMetadata)
	t.u16(uint16(len(r.columns)))
	for _, c := range r.columns {
		t.u32(0)      // user type
		t.u16(0x0001) // nullable
		if r.ints {
			t.u8(typeIntN)
			t.u8(4)
		} else {
			t.u8(typeNVarChar)
			t.u16(256)
			t.WriteString(fixedCollationLatin1)
		}
		t.bVarChar(c)
	}
	for _, row := range r.rows {
		t.u8(tokRow)
		for _, v := range row {
			if r.ints {
				t.u8(4)
				var n int32
				_, _ = fmt.Sscan(v, &n)
				t.u32(uint32(n))
				continue
			}
			b := toUCS2(v)
			t.u16(uint16(len(b)))
			t.Write(b)
		}
	}
}

// batchText reads a SQL batch payload: ALL_HEADERS, then the text.
func batchText(p []byte) (string, error) {
	if len(p) < 4 {
		return "", errors.New("short SQL batch")
	}
	n := int(binary.LittleEndian.Uint32(p))
	if n > len(p) {
		return "", errors.New("SQL batch headers past the end")
	}
	return ucs2(p[n:]), nil
}

// rpcCall reads an RPC payload calling sp_executesql: the statement and
// its named nvarchar parameters.
func rpcCall(p []byte) (string, map[string]string, error) {
	if len(p) < 4 {
		return "", nil, errors.New("short RPC")
	}
	at := int(binary.LittleEndian.Uint32(p))
	need := func(n int) error {
		if at+n > len(p) {
			return errors.New("RPC past the end")
		}
		return nil
	}
	if err := need(6); err != nil {
		return "", nil, err
	}
	if binary.LittleEndian.Uint16(p[at:]) != 0xFFFF || binary.LittleEndian.Uint16(p[at+2:]) != procIDSpExecuteSQL {
		return "", nil, &sqlError{Number: 2812, Class: 16, Msg: "Could not find stored procedure (the fake runs sp_executesql only)."}
	}
	at += 6 // id switch, proc id, option flags
	var values []string
	var names []string
	for at < len(p) {
		if err := need(1); err != nil {
			return "", nil, err
		}
		nameLen := int(p[at]) * 2
		at++
		if err := need(nameLen + 2); err != nil {
			return "", nil, err
		}
		name := ucs2(p[at : at+nameLen])
		at += nameLen
		at++ // status flags
		typ := p[at]
		at++
		if typ != typeNVarChar {
			return "", nil, &sqlError{Number: 8016, Class: 16, Msg: fmt.Sprintf("parameter %q has type %#x; the fake takes nvarchar only", name, typ)}
		}
		if err := need(7); err != nil {
			return "", nil, err
		}
		maxLen := binary.LittleEndian.Uint16(p[at:])
		at += 7 // max length, collation
		var value []byte
		if maxLen == shortLenMax {
			if err := need(8); err != nil {
				return "", nil, err
			}
			total := binary.LittleEndian.Uint64(p[at:])
			at += 8
			if total != plpNull {
				for {
					if err := need(4); err != nil {
						return "", nil, err
					}
					n := int(binary.LittleEndian.Uint32(p[at:]))
					at += 4
					if n == 0 {
						break
					}
					if err := need(n); err != nil {
						return "", nil, err
					}
					value = append(value, p[at:at+n]...)
					at += n
				}
			}
		} else {
			if err := need(2); err != nil {
				return "", nil, err
			}
			n := int(binary.LittleEndian.Uint16(p[at:]))
			at += 2
			if n != shortLenMax {
				if err := need(n); err != nil {
					return "", nil, err
				}
				value = p[at : at+n]
				at += n
			}
		}
		names = append(names, name)
		values = append(values, ucs2(value))
	}
	if len(values) < 1 {
		return "", nil, errors.New("sp_executesql without a statement")
	}
	params := map[string]string{}
	for i := 2; i < len(values); i++ {
		params[names[i]] = values[i]
	}
	return values[0], params, nil
}
