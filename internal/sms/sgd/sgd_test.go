// Copyright 2026 3eAI Labs Ltd
// SPDX-License-Identifier: AGPL-3.0-only

package sgd

import (
	"bytes"
	"encoding/json"
	"testing"
	"time"

	"github.com/3eAI-Labs/diameter-ha-gateway/internal/diameter"
	"github.com/3eAI-Labs/diameter-ha-gateway/internal/diamtest"
	"github.com/fiorix/go-diameter/v4/diam"
	"github.com/fiorix/go-diameter/v4/diam/avp"
	"github.com/fiorix/go-diameter/v4/diam/datatype"
	"github.com/fiorix/go-diameter/v4/diam/dict"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// wire serialises m and reads it back with the gateway's dictionary, as the
// server and a peer do. The header's Message-Length must match the bytes.
func wire(t *testing.T, m *diam.Message) *diam.Message {
	t.Helper()
	b, err := m.Serialize()
	require.NoError(t, err)
	require.Equal(t, int(m.Header.MessageLength), len(b), "Message-Length matches the serialised size")
	out, err := diam.ReadMessage(bytes.NewReader(b), dict.Default)
	require.NoError(t, err)
	assertTyped(t, out.AVP)
	return out
}

// assertTyped fails for an AVP, at any depth, that decoded as Unknown: one
// the dictionary does not define for the message's application.
func assertTyped(t *testing.T, list []*diam.AVP) {
	t.Helper()
	for _, a := range list {
		_, unknown := a.Data.(datatype.Unknown)
		assert.False(t, unknown, "AVP %d decoded as Unknown", a.Code)
		if g, ok := a.Data.(*diam.GroupedAVP); ok {
			assertTyped(t, g.AVP)
		}
	}
}

// request builds a request from the network: the common AVPs, then list.
func request(code, app uint32, list ...*diam.AVP) *diam.Message {
	m := newRequest(code, app, "mme.example;1;2;x", "mme.example", "example", "gw.example", "example")
	add(m, list...)
	return m
}

func flagsOf(t *testing.T, l avpList, code uint32) uint8 {
	t.Helper()
	a := l.get(code)
	require.NotNil(t, a, "AVP %d", code)
	return a.Flags
}

func u(v uint32) *uint32 { return &v }

func TestTBCD(t *testing.T) {
	for digits, octets := range map[string][]byte{
		"905551234567":  {0x09, 0x55, 0x15, 0x32, 0x54, 0x76},
		"90555123456":   {0x09, 0x55, 0x15, 0x32, 0x54, 0xF6},
		"":              {},
		"12*#":          {0x21, 0xBA},
		"+905309999999": {0x09, 0x35, 0x90, 0x99, 0x99, 0x99},
	} {
		assert.Equal(t, octets, encodeTBCD(digits), digits)
	}
	assert.Equal(t, "90555123456", decodeTBCD([]byte{0x09, 0x55, 0x15, 0x32, 0x54, 0xF6}))
}

// TestCommands_DecodeFromWire: every command of S6c (16777312) and SGd
// (16777313), request and answer, decodes with its AVPs typed, under the
// Application-Id TS 29.338 gives it. Up to 0.11.0 all four commands were
// under 16777312 without AVP rules, so none decoded and go-diameter closed
// the connection.
func TestCommands_DecodeFromWire(t *testing.T) {
	sn := &ServingNode{MMEName: "mme.example", MMERealm: "example", MMENumberForMTSMS: "905320000001"}
	cases := map[string]*diam.Message{
		"SRR": buildSRR(&SRRRequest{MSISDN: "905551234567", SCAddress: "905309999999", SMRPMTI: u(0)},
			"s", "gw", "r", "hss", "r"),
		"TFR": buildTFR(&TFRRequest{IMSI: "286011234567890", SCAddress: "905309999999", SMRPUI: []byte{1, 2, 3},
			MMENumberForMTSMS: "905320000001", SMDeliveryStartTime: 1792300000}, "s", "gw", "r", "mme", "r"),
		"ALR": buildALR(&ALRRequest{subscriber: subscriber{MSISDN: "905551234567"}, SCAddress: "905309999999",
			ServingNode: sn, MaximumUEAvailabilityTime: 1792300000}, "s", "gw", "r", "", "r"),
		"RDR": buildRDR(&RDRRequest{subscriber: subscriber{UserName: "286011234567890"}, SCAddress: "905309999999",
			SMDeliveryOutcome: &SMDeliveryOutcome{MME: &DeliveryOutcome{Cause: u(1), AbsentUserDiagnosticSM: u(0)}}},
			"s", "gw", "r", "", "r"),
		"OFR": request(CommandOFR, AppIDSGd,
			wSGd.tbcd(avpSCAddress, "905309999999"),
			wSGd.userIdentifier(&UserIdentifier{MSISDN: "905551234567"}),
			wSGd.octets(avpSMRPUI, []byte{0x11, 0x22})),
	}
	apps := map[string]uint32{"SRR": AppIDS6c, "ALR": AppIDS6c, "RDR": AppIDS6c, "TFR": AppIDSGd, "OFR": AppIDSGd}
	for name, m := range cases {
		got := wire(t, m)
		assert.Equal(t, apps[name], got.Header.ApplicationID, name)
		assert.Equal(t, uint8(diam.RequestFlag|diam.ProxiableFlag), got.Header.CommandFlags, "%s: REQ, PXY", name)
		assert.Nil(t, avpList(got.AVP).base(avp.VendorSpecificApplicationID), "%s: no VSAI (§5.3.2.2)", name)

		ans := wire(t, diameter.CompleteAnswer(answerFor(got, Result{ResultCode: 2001}), got, "gw", "r"))
		assert.Equal(t, apps[name], ans.Header.ApplicationID, name+" answer")
	}
}

// TestFlags_PerApplication: an AVP takes the M bit its application gives
// it. MME-Number-for-MT-SMS is "Must" in S6c and V only in SGd; SGSN-Number
// is "Must not" in SGd (Tables 5.3.3.1/2, 6.3.3.1/2).
func TestFlags_PerApplication(t *testing.T) {
	tfr := avpList(buildTFR(&TFRRequest{IMSI: "1", SCAddress: "1", SMRPUI: []byte{1},
		MMENumberForMTSMS: "1", SGSNNumber: "1", MaximumRetransmissionTime: 1}, "s", "gw", "r", "mme", "r").AVP)
	assert.Equal(t, uint8(avp.Vbit), flagsOf(t, tfr, avpMMENumberForMTSMS))
	assert.Equal(t, uint8(avp.Vbit), flagsOf(t, tfr, avpSGSNNumber))
	assert.Equal(t, uint8(avp.Vbit), flagsOf(t, tfr, avpMaxRetransmissionTime))
	assert.Equal(t, uint8(avp.Mbit|avp.Vbit), flagsOf(t, tfr, avpSCAddress))

	sn := children(wS6c.servingNode(avpServingNode, &ServingNode{MMENumberForMTSMS: "1", SGSNName: "s"}))
	assert.Equal(t, uint8(avp.Mbit|avp.Vbit), flagsOf(t, sn, avpMMENumberForMTSMS))
	assert.Equal(t, uint8(avp.Vbit), flagsOf(t, sn, avpSGSNName), "TS 29.173: SGSN-Name V, must not M")
}

// TestDecodeOFR: an OFR from the wire becomes the event, numbers decoded
// from TBCD. Up to 0.11.0 decoding an OFR panicked.
func TestDecodeOFR(t *testing.T) {
	m := wire(t, request(CommandOFR, AppIDSGd,
		wSGd.tbcd(avpSCAddress, "905309999999"),
		wSGd.u32(avpOFRFlags, 1),
		wSGd.userIdentifier(&UserIdentifier{MSISDN: "905551234567", UserName: "286011234567890"}),
		wSGd.octets(avpSMRPUI, []byte{0x11, 0x22}),
		wSGd.smDeliveryOutcome(&SMDeliveryOutcome{MSC: &DeliveryOutcome{Cause: u(2)}})))
	e := decodeOFR(m, "t")
	assert.Equal(t, "905309999999", e.SCAddress)
	assert.Equal(t, uint32(1), e.OFRFlags)
	assert.Equal(t, &UserIdentifier{MSISDN: "905551234567", UserName: "286011234567890"}, e.UserIdentifier)
	assert.Equal(t, []byte{0x11, 0x22}, e.SMRPUI)
	require.NotNil(t, e.SMDeliveryOutcome)
	assert.Equal(t, u(2), e.SMDeliveryOutcome.MSC.Cause)
	assert.Equal(t, "mme.example", e.Header.OriginHost)
}

// TestEncodeAnswers: the application's reply becomes the answer. A 3GPP
// result travels in Experimental-Result without Result-Code (§7.3.1).
func TestEncodeAnswers(t *testing.T) {
	tfr := wire(t, request(CommandTFR, AppIDSGd))
	tfa := wire(t, diameter.CompleteAnswer(encodeTFA(tfr, &TFAAnswer{
		Result:                 Result{ExperimentalResult: &ExperimentalResult{ExperimentalResultCode: DiameterErrorAbsentUser}},
		AbsentUserDiagnosticSM: u(0),
		SMDeliveryFailureCause: &SMDeliveryFailureCause{Cause: 0},
	}), tfr, "gw", "r"))
	assert.Nil(t, avpList(tfa.AVP).base(avp.ResultCode))
	r := readResult(tfa)
	assert.Equal(t, uint32(5550), r.ResultCode)
	got := decodeTFA(tfa)
	assert.Equal(t, u(0), got.AbsentUserDiagnosticSM, "diagnostic 0 is sent")
	require.NotNil(t, got.SMDeliveryFailureCause)
	assert.Equal(t, uint32(0), got.SMDeliveryFailureCause.Cause, "MEMORY_CAPACITY_EXCEEDED (0) is sent")

	srr := wire(t, request(CommandSRR, AppIDS6c))
	sra := wire(t, diameter.CompleteAnswer(encodeSRA(srr, &SRAAnswer{
		Result:      Result{ResultCode: 2001},
		UserName:    "286011234567890",
		ServingNode: &ServingNode{MMEName: "mme.example", MMERealm: "example", MMENumberForMTSMS: "905320000001"},
		MWDStatus:   2,
	}), srr, "gw", "r"))
	a := decodeSRA(sra)
	assert.Equal(t, uint32(2001), a.ResultCode)
	assert.Equal(t, "286011234567890", a.UserName)
	assert.Equal(t, "mme.example", a.ServingNode.MMEName)
	assert.Equal(t, "905320000001", a.ServingNode.MMENumberForMTSMS)
	assert.Equal(t, "mme.example", a.MMEName, "copied for applications of earlier releases")
	assert.Equal(t, "example", a.MMERealm)
	assert.Equal(t, uint32(2), a.MWDStatus)
}

// fakeRequester answers every NATS request with reply and records the
// event.
type fakeRequester struct {
	reply   string
	subject string
	event   []byte
}

func (f *fakeRequester) Request(subject string, v any, _ time.Duration) ([]byte, error) {
	f.subject = subject
	f.event, _ = json.Marshal(v)
	return []byte(f.reply), nil
}

func serveOnce(t *testing.T, h *Handler, handle func(diam.Conn, *diam.Message), m *diam.Message) *diam.Message {
	t.Helper()
	c := &diamtest.Conn{}
	handle(c, wire(t, m))
	out, err := c.Messages()
	require.NoError(t, err)
	require.Len(t, out, 1)
	return out[0]
}

// TestHandler_RoundTrip: an RDR, which the gateway did not serve before, is
// published on diameter.sgd.rdr and answered with the application's reply.
func TestHandler_RoundTrip(t *testing.T) {
	f := &fakeRequester{reply: `{"result_code":2001}`}
	h := NewHandler(f, diameter.NewPeerManager(), time.Second)
	rda := serveOnce(t, h, h.HandleRDR, request(CommandRDR, AppIDS6c,
		wS6c.userIdentifier(&UserIdentifier{MSISDN: "905551234567"}),
		wS6c.tbcd(avpSCAddress, "905309999999"),
		wS6c.smDeliveryOutcome(&SMDeliveryOutcome{MME: &DeliveryOutcome{Cause: u(1)}})))

	assert.Equal(t, TopicSGdRDR, f.subject)
	var e RDREvent
	require.NoError(t, json.Unmarshal(f.event, &e))
	assert.Equal(t, "905551234567", e.UserIdentifier.MSISDN)
	assert.Equal(t, u(1), e.SMDeliveryOutcome.MME.Cause)
	assert.Equal(t, uint32(2001), readResult(rda).ResultCode)
	assert.Equal(t, AppIDS6c, rda.Header.ApplicationID)
}

// TestHandler_MissingMandatoryAVP: a TFR without its mandatory User-Name and
// SM-RP-UI (§6.3.2.5) is answered 5005 with a Failed-AVP and not published.
func TestHandler_MissingMandatoryAVP(t *testing.T) {
	f := &fakeRequester{reply: `{"result_code":2001}`}
	h := NewHandler(f, diameter.NewPeerManager(), time.Second)
	tfa := serveOnce(t, h, h.HandleTFR, request(CommandTFR, AppIDSGd, wSGd.tbcd(avpSCAddress, "1")))

	assert.Empty(t, f.subject, "not published")
	assert.Equal(t, uint32(diam.MissingAVP), readResult(tfa).ResultCode)
	failed := children(avpList(tfa.AVP).base(avp.FailedAVP))
	var codes []uint32
	for _, a := range failed {
		codes = append(codes, a.Code)
	}
	assert.ElementsMatch(t, []uint32{avpUserName, avpSMRPUI}, codes)
}

// TestHandler_BadReply: a reply that is not JSON is answered 5012.
func TestHandler_BadReply(t *testing.T) {
	f := &fakeRequester{reply: `not json`}
	h := NewHandler(f, diameter.NewPeerManager(), time.Second)
	ala := serveOnce(t, h, h.HandleALR, request(CommandALR, AppIDS6c,
		wS6c.tbcd(avpSCAddress, "1"), wS6c.userIdentifier(&UserIdentifier{MSISDN: "1"})))
	assert.Equal(t, uint32(diam.UnableToComply), readResult(ala).ResultCode)
}

// TestAnswerFor_3GPPCodeAsResultCode: an application that answers with a TS
// 29.338 code in result_code, as the gateway reports a peer's
// Experimental-Result to it, has it sent in Experimental-Result (§7.3.1).
// 5001 stays a Result-Code (RFC 6733 DIAMETER_AVP_UNSUPPORTED).
func TestAnswerFor_3GPPCodeAsResultCode(t *testing.T) {
	req := wire(t, request(CommandTFR, AppIDSGd))
	ans := avpList(answerFor(req, Result{ResultCode: DiameterErrorAbsentUser}).AVP)
	assert.Nil(t, ans.base(avp.ResultCode))
	assert.NotNil(t, ans.base(avp.ExperimentalResult))

	ans = avpList(answerFor(req, Result{ResultCode: 5001}).AVP)
	assert.NotNil(t, ans.base(avp.ResultCode))
}

// TestBuild_EmptyValuesLeaveMandatoryAVPsOut: an empty group or a number
// with no digit writes no AVP, and missingAVPs finds the hole, so the client
// replies 5005 instead of sending a request without a mandatory AVP.
func TestBuild_EmptyValuesLeaveMandatoryAVPsOut(t *testing.T) {
	rdr := buildRDR(&RDRRequest{subscriber: subscriber{UserIdentifier: &UserIdentifier{}},
		SCAddress: "xyz", SMDeliveryOutcome: &SMDeliveryOutcome{MME: &DeliveryOutcome{}}},
		"s", "gw", "r", "", "r")
	var codes []uint32
	for _, f := range missingAVPs(rdr, "RDR") {
		codes = append(codes, f.Code)
	}
	assert.ElementsMatch(t, []uint32{avpUserIdentifier, avpSCAddress, avpSMDeliveryOutcome}, codes)
}
