// Copyright 2026 3eAI Labs Ltd
// SPDX-License-Identifier: AGPL-3.0-only

package diameter

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
