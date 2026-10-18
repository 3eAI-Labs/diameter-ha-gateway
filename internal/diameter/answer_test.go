// Copyright 2026 3eAI Labs Ltd
// SPDX-License-Identifier: AGPL-3.0-only

package diameter

import (
	"testing"

	"github.com/fiorix/go-diameter/v4/diam"
	"github.com/fiorix/go-diameter/v4/diam/avp"
	"github.com/fiorix/go-diameter/v4/diam/datatype"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestCompleteAnswer_AddsWhatEveryAnswerMustCarry(t *testing.T) {
	req := buildValidULR(t)
	req.NewAVP(avp.AuthSessionState, avp.Mbit, 0, datatype.Enumerated(1))

	ans := CompleteAnswer(req.Answer(diam.Success), req, "gw.example", "example")

	require.NotEmpty(t, ans.AVP)
	assert.Equal(t, uint32(avp.SessionID), ans.AVP[0].Code, "Session-Id first (RFC 6733 §8.8)")
	assert.Equal(t, "test-session-123", AVPString(ans.AVP[0]))
	assert.Equal(t, "gw.example", AVPString(topLevelAVP(ans, avp.OriginHost)))
	assert.Equal(t, "example", AVPString(topLevelAVP(ans, avp.OriginRealm)))
	assert.Equal(t, datatype.Enumerated(1), topLevelAVP(ans, avp.AuthSessionState).Data)
	rc, _ := AnswerResult(ans)
	assert.Equal(t, uint32(2001), rc)

	raw, err := ans.Serialize()
	require.NoError(t, err)
	assert.Equal(t, int(ans.Header.MessageLength), len(raw), "header length matches the AVPs")
}

func TestCompleteAnswer_KeepsWhatTheAnswerHas(t *testing.T) {
	req := buildValidULR(t)
	ans := req.Answer(diam.Success)
	ans.NewAVP(avp.OriginHost, avp.Mbit, 0, datatype.DiameterIdentity("hss.example"))

	CompleteAnswer(ans, req, "gw.example", "example")
	CompleteAnswer(ans, req, "gw.example", "example") // idempotent

	n := 0
	for _, a := range ans.AVP {
		if a.Code == avp.OriginHost || a.Code == avp.SessionID {
			n++
		}
	}
	assert.Equal(t, 2, n, "one Origin-Host and one Session-Id")
	assert.Equal(t, "hss.example", AVPString(topLevelAVP(ans, avp.OriginHost)))
}

// TestCompleteAnswer_ProxyInfoAndFlags: every Proxy-Info of the request is
// echoed in order (RFC 6733 §6.2), the T flag copied from a retransmitted
// request is cleared (§3), and a 3xxx answer carries the E flag (§7.1.3).
// Up to 0.10.3 none of the three held.
func TestCompleteAnswer_ProxyInfoAndFlags(t *testing.T) {
	req := buildValidULR(t)
	req.Header.CommandFlags |= diam.RetransmittedFlag
	for _, host := range []string{"dra1.example", "dra2.example"} {
		req.NewAVP(avp.ProxyInfo, avp.Mbit, 0, &diam.GroupedAVP{AVP: []*diam.AVP{
			diam.NewAVP(avp.ProxyHost, avp.Mbit, 0, datatype.DiameterIdentity(host)),
			diam.NewAVP(avp.ProxyState, avp.Mbit, 0, datatype.OctetString(host)),
		}})
	}

	for rc, wantE := range map[uint32]bool{diam.Success: false, diam.UnableToDeliver: true, diam.UnableToComply: false} {
		ans := CompleteAnswer(req.Answer(rc), req, "gw.example", "example")
		decoded := fromWire(t, ans)

		var hosts []string
		for _, a := range decoded.AVP {
			if a.Code == avp.ProxyInfo {
				for _, c := range a.Data.(*diam.GroupedAVP).AVP {
					if c.Code == avp.ProxyHost {
						hosts = append(hosts, AVPString(c))
					}
				}
			}
		}
		assert.Equal(t, []string{"dra1.example", "dra2.example"}, hosts, "rc %d", rc)
		assert.Zero(t, decoded.Header.CommandFlags&diam.RetransmittedFlag, "rc %d: T flag", rc)
		assert.Equal(t, wantE, decoded.Header.CommandFlags&diam.ErrorFlag != 0, "rc %d: E flag", rc)
	}
}

// TestCompleteAnswer_NoResultAnswered5012: an application reply without a
// result code produced an answer with neither Result-Code nor
// Experimental-Result, which every answer ABNF requires.
func TestCompleteAnswer_NoResultAnswered5012(t *testing.T) {
	req := buildValidULR(t)
	ans := CompleteAnswer(req.Answer(0), req, "gw.example", "example")
	rc, _ := AnswerResult(ans)
	assert.Equal(t, uint32(diam.UnableToComply), rc)

	exp := req.Answer(0)
	exp.NewAVP(avp.ExperimentalResult, avp.Mbit, 0, &diam.GroupedAVP{AVP: []*diam.AVP{
		diam.NewAVP(avp.VendorID, avp.Mbit, 0, datatype.Unsigned32(10415)),
		diam.NewAVP(avp.ExperimentalResultCode, avp.Mbit, 0, datatype.Unsigned32(5001)),
	}})
	CompleteAnswer(exp, req, "gw.example", "example")
	assert.Nil(t, topLevelAVP(exp, avp.ResultCode), "an Experimental-Result answer gets no Result-Code")
}

// TestGatewayRequests_Proxiable: the S6a requests the gateway sends are
// "REQ, PXY" (TS 29.272 §7.2.x); go-diameter's NewRequest set R only.
func TestGatewayRequests_Proxiable(t *testing.T) {
	tr := NewTranslator()
	clr, _ := tr.CreateCLR("001010000000001", "mme", "r", "gw", "r", 0)
	rsr, _ := tr.CreateRSR("00101", "mme", "r", "gw", "r")
	dsr, _ := tr.CreateDSR("001010000000001", "mme", "r", "gw", "r", 1, nil)
	idr, _ := tr.CreateIDR("001010000000001", "mme", "r", "gw", "r", nil, "", 0)
	for _, m := range []*diam.Message{clr, rsr, dsr, idr} {
		flags := fromWire(t, m).Header.CommandFlags
		assert.Equal(t, uint8(diam.RequestFlag|diam.ProxiableFlag), flags, "command %d", m.Header.CommandCode)
	}
}

// TestCreateDPA_NoTFlag: the DPA is built without CompleteAnswer, and
// go-diameter's Answer copies the T flag of a retransmitted DPR.
func TestCreateDPA_NoTFlag(t *testing.T) {
	dpr := NewTranslator().CreateDPR("mme.example", "example", 0)
	dpr.Header.CommandFlags |= diam.RetransmittedFlag
	dpa := NewTranslator().CreateDPA(dpr, "gw.example", "example")
	assert.Zero(t, dpa.Header.CommandFlags&diam.RetransmittedFlag)
}
