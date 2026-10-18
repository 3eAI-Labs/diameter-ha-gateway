// Copyright 2026 3eAI Labs Ltd
// SPDX-License-Identifier: AGPL-3.0-only

package charging

import (
	"bytes"
	"testing"

	"github.com/fiorix/go-diameter/v4/diam"
	"github.com/fiorix/go-diameter/v4/diam/avp"
	"github.com/fiorix/go-diameter/v4/diam/datatype"
	"github.com/fiorix/go-diameter/v4/diam/dict"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func reread(t *testing.T, m *diam.Message) *diam.Message {
	t.Helper()
	b, err := m.Serialize()
	require.NoError(t, err)
	out, err := diam.ReadMessage(bytes.NewReader(b), dict.Default)
	require.NoError(t, err)
	for _, a := range out.AVP {
		_, unknown := a.Data.(datatype.Unknown)
		assert.False(t, unknown, "AVP %d/%d decoded as Unknown", a.Code, a.VendorID)
	}
	return out
}

// TestBuildRAR: the RAR of TS 32.299 §6.4.4 — Session-Id first (RFC 6733
// §8.3.1), R and P flags, Application-Id 4 in the header and in
// Auth-Application-Id, Re-Auth-Request-Type AUTHORIZE_ONLY (0) written,
// and Rating-Group 0 written when set.
func TestBuildRAR(t *testing.T) {
	sub := uint64(3)
	m := reread(t, BuildRAR(&ReAuthRequest{
		Header:   DiameterHeader{SessionID: "pgw;1;2"},
		UserName: "001010000000001", CCSubSessionID: &sub, RatingGroup: p32(0), ServiceIdentifier: p32(7),
	}, "pgw.example", "example", "gw.example", "gw-realm"))

	assert.Equal(t, uint32(CommandRAR), m.Header.CommandCode)
	assert.Equal(t, AppIDCreditControl, m.Header.ApplicationID)
	assert.Equal(t, uint8(diam.RequestFlag|diam.ProxiableFlag), m.Header.CommandFlags)
	require.NotEmpty(t, m.AVP)
	assert.Equal(t, uint32(avp.SessionID), m.AVP[0].Code, "Session-Id first")
	assert.Equal(t, "pgw;1;2", text(m.AVP[0]))
	assert.Equal(t, p32(AppIDCreditControl), u32p(pick(m.AVP, avp.AuthApplicationID, 0)))
	assert.Equal(t, p32(0), u32p(pick(m.AVP, codeReAuthRequestType, 0)), "AUTHORIZE_ONLY sent")
	assert.Equal(t, "001010000000001", text(pick(m.AVP, avp.UserName, 0)))
	assert.Equal(t, uint64(3), u64(pick(m.AVP, codeCCSubSessionID, 0)))
	assert.Equal(t, p32(0), u32p(pick(m.AVP, codeRatingGroup, 0)))
	assert.Equal(t, p32(7), u32p(pick(m.AVP, codeServiceIdentifier, 0)))
	assert.Nil(t, pick(m.AVP, codeGSUPoolIdentifier, 0), "absent stays absent")
	assert.Equal(t, uint32(0), m.AVP[0].VendorID)

	a, err := m.FindAVP(avp.DestinationHost, 0)
	require.NoError(t, err)
	assert.Equal(t, datatype.DiameterIdentity("pgw.example"), a.Data)
}

// TestBuildASR: RFC 6733 §8.5.1 — the base header, no Re-Auth-Request-Type.
func TestBuildASR(t *testing.T) {
	m := reread(t, BuildASR(&AbortSessionRequest{Header: DiameterHeader{SessionID: "ctf;9"}},
		"ctf.example", "example", "gw.example", "gw-realm"))
	assert.Equal(t, uint32(CommandASR), m.Header.CommandCode)
	assert.Equal(t, AppIDCreditControl, m.Header.ApplicationID)
	assert.Equal(t, "ctf;9", text(m.AVP[0]))
	assert.Nil(t, pick(m.AVP, codeReAuthRequestType, 0))
	assert.Nil(t, pick(m.AVP, avp.UserName, 0))
}

// TestReadAbortAnswer: the client's identity and RAA identifiers reach the
// OCS; an Experimental-Result-Code is reported in result_code too.
func TestReadAbortAnswer(t *testing.T) {
	raa := diam.NewMessage(CommandRAR, diam.ProxiableFlag, AppIDCreditControl, 1, 2, dict.Default)
	raa.NewAVP(avp.SessionID, avp.Mbit, 0, datatype.UTF8String("pgw;1;2"))
	raa.NewAVP(avp.ResultCode, avp.Mbit, 0, datatype.Unsigned32(2002))
	raa.NewAVP(avp.OriginHost, avp.Mbit, 0, datatype.DiameterIdentity("pgw.example"))
	raa.NewAVP(avp.OriginRealm, avp.Mbit, 0, datatype.DiameterIdentity("example"))
	raa.NewAVP(avp.ErrorMessage, 0, 0, datatype.UTF8String("rating group unknown"))
	raa.AddAVP(cc(codeRatingGroup, datatype.Unsigned32(0)))
	raa.AddAVP(cc(codeCCSubSessionID, datatype.Unsigned64(3)))
	got := ReadAbortAnswer(reread(t, raa), 2002, 0)
	sub := uint64(3)
	assert.Equal(t, AbortAnswer{ResultCode: 2002, ErrorMessage: "rating group unknown", OriginHost: "pgw.example", OriginRealm: "example",
		CCSubSessionID: &sub, RatingGroup: p32(0)}, got)

	assert.Equal(t, uint32(5030), ReadAbortAnswer(raa, 0, 5030).ResultCode)
}

type binder struct{ bound, unbound []string }

func (b *binder) Bind(s, p string)   { b.bound = append(b.bound, s+"@"+p) }
func (b *binder) Unbind(s, p string) { b.unbound = append(b.unbound, s+"@"+p) }

// TestTrackSession: INITIAL and UPDATE bind, TERMINATION unbinds, EVENT
// leaves no session to reach (RFC 8506 §5.2).
func TestTrackSession(t *testing.T) {
	b := &binder{}
	TrackSession(b, CCRequestTypeInitial, "s", "p")
	TrackSession(b, CCRequestTypeUpdate, "s", "p")
	TrackSession(b, CCRequestTypeEvent, "e", "p")
	TrackSession(b, CCRequestTypeTermination, "s", "p")
	TrackSession(b, CCRequestTypeInitial, "s", "")
	TrackSession(nil, CCRequestTypeInitial, "s", "p")
	assert.Equal(t, []string{"s@p", "s@p"}, b.bound)
	assert.Equal(t, []string{"s@p"}, b.unbound)
}
