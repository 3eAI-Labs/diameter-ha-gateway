// Copyright 2026 3eAI Labs Ltd
// SPDX-License-Identifier: AGPL-3.0-only

package diameter

import (
	"bytes"
	"testing"

	pb "github.com/3eAI-Labs/diameter-ha-gateway-client"

	"github.com/fiorix/go-diameter/v4/diam"
	"github.com/fiorix/go-diameter/v4/diam/avp"
	"github.com/fiorix/go-diameter/v4/diam/datatype"
	"github.com/fiorix/go-diameter/v4/diam/dict"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// fromWire serialises m and decodes it the way the server reads a request, so
// each AVP takes the type the dictionary gives it rather than the one the
// test built it with.
func fromWire(t *testing.T, m *diam.Message) *diam.Message {
	t.Helper()
	wire, err := m.Serialize()
	require.NoError(t, err)
	decoded, err := diam.ReadMessage(bytes.NewReader(wire), dict.Default)
	require.NoError(t, err)
	return decoded
}

func s6aRequest(code uint32) *diam.Message {
	m := diam.NewRequest(code, 16777251, dict.Default)
	m.NewAVP(avp.SessionID, avp.Mbit, 0, datatype.UTF8String("mme.example;1"))
	m.NewAVP(avp.AuthSessionState, avp.Mbit, 0, datatype.Enumerated(1))
	m.NewAVP(avp.OriginHost, avp.Mbit, 0, datatype.DiameterIdentity("mme.example"))
	m.NewAVP(avp.OriginRealm, avp.Mbit, 0, datatype.DiameterIdentity("example"))
	m.NewAVP(avp.DestinationRealm, avp.Mbit, 0, datatype.DiameterIdentity("example"))
	m.NewAVP(avp.UserName, avp.Mbit, 0, datatype.UTF8String("001010000000001"))
	return m
}

// UE-SRVCC-Capability is Enumerated (TS 29.272 §7.3.130); read as Unsigned32
// it panicked and closed the MME's connection.
func TestToS6aEvent_UESRVCCCapabilityFromWire(t *testing.T) {
	m := s6aRequest(diam.UpdateLocation)
	m.NewAVP(avp.UESRVCCCapability, avp.Mbit|avp.Vbit, 10415, datatype.Enumerated(1)) // UE-SRVCC-SUPPORTED

	event, err := NewTranslator().ToS6aEvent(fromWire(t, m), "ulr-wire")
	require.NoError(t, err)
	assert.Equal(t, uint32(1), event.UE_SRVCC)
}

// Alert-Reason (1434, Enumerated, §7.3.83) was not in the dictionary: it
// decoded as Unknown and the NOR handler panicked.
func TestToS6aEvent_AlertReasonFromWire(t *testing.T) {
	m := s6aRequest(diam.Notify)
	m.NewAVP(1434, avp.Mbit|avp.Vbit, 10415, datatype.Enumerated(1)) // UE_MEMORY_AVAILABLE

	event, err := NewTranslator().ToS6aEvent(fromWire(t, m), "nor-wire")
	require.NoError(t, err)
	assert.Equal(t, uint32(1), event.AlertReason)
}

// TestULAFlags_OnlyOnSuccess: ULA-Flags and PUA-Flags "shall be present
// only when the Result-Code AVP is DIAMETER_SUCCESS" (TS 29.272 Tables
// 5.2.1.1.1/2, 5.2.1.3.1/2); a reply that failed carried them too.
func TestULAFlags_OnlyOnSuccess(t *testing.T) {
	tr := NewTranslator()
	for _, tc := range []struct {
		code, flagsAVP uint32
		reply          string
		want           bool
	}{
		{316, avp.ULAFlags, `{"result_code":2001,"ula_flags":1}`, true},
		{316, avp.ULAFlags, `{"result_code":5012,"ula_flags":1}`, false},
		{316, avp.ULAFlags, `{"experimental_result_code":5001,"ula_flags":1}`, false},
		{321, avp.PUAFlags, `{"result_code":2001,"pua_flags":1}`, true},
		{321, avp.PUAFlags, `{"experimental_result_code":5001,"pua_flags":1}`, false},
	} {
		ans, err := tr.ToDiameterAnswer([]byte(tc.reply), s6aRequest(tc.code))
		require.NoError(t, err)
		_, err = fromWire(t, ans).FindAVP(tc.flagsAVP, vendorID3GPP)
		assert.Equal(t, tc.want, err == nil, "%d %s", tc.code, tc.reply)
	}
}

// TestCreateIDR_EmptySubscriptionData: an IDR that only requests state or
// location carries an empty Subscription-Data, which is mandatory (TS
// 29.272 §5.2.2.1.3); it was left out.
func TestCreateIDR_EmptySubscriptionData(t *testing.T) {
	for name, profile := range map[string]*pb.SubscriptionProfile{"nil": nil, "empty": {}} {
		m, _ := NewTranslator().CreateIDR("001010000000001", "mme", "r", "hss", "r", profile, "", 4)
		sd, err := fromWire(t, m).FindAVP(avp.SubscriptionData, vendorID3GPP)
		require.NoError(t, err, name)
		assert.Empty(t, sd.Data.(*diam.GroupedAVP).AVP, name)
	}
}

// TestCreateCLR_CancellationType: the CLR carries the application's
// cancellation_type (TS 29.272 §7.3.24); it was always 0.
func TestCreateCLR_CancellationType(t *testing.T) {
	m, _ := NewTranslator().CreateCLR("001010000000001", "mme", "r", "hss", "r", 2) // SUBSCRIPTION_WITHDRAWAL
	ct, err := fromWire(t, m).FindAVP(avp.CancellationType, vendorID3GPP)
	require.NoError(t, err)
	assert.Equal(t, datatype.Enumerated(2), ct.Data)
}
