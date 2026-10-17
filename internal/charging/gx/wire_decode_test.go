// Copyright 2026 3eAI Labs Ltd
// SPDX-License-Identifier: AGPL-3.0-only

package gx

import (
	"bytes"
	"testing"

	"github.com/3eAI-Labs/diameter-ha-gateway/internal/charging"
	"github.com/fiorix/go-diameter/v4/diam"
	"github.com/fiorix/go-diameter/v4/diam/avp"
	"github.com/fiorix/go-diameter/v4/diam/datatype"
	"github.com/fiorix/go-diameter/v4/diam/dict"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestToCCREvent_FromWire decodes a CCR the way the server reads it, so each
// AVP takes the type the dictionary gives it. Bearer-Operation, AN-GW-Status
// and PDN-Connection-ID were missing from the dictionary: they decoded as
// Unknown, the translator's type assertion panicked and the PCEF's connection
// was closed.
func TestToCCREvent_FromWire(t *testing.T) {
	m := buildTestGxCCR(t, charging.CCRequestTypeUpdate, 1)
	m.NewAVP(avpBearerOperation, avp.Mbit|avp.Vbit, vendorID3GPP, datatype.Enumerated(BearerOperationModification))
	m.NewAVP(avpANGWStatus, avp.Vbit, vendorID3GPP, datatype.Enumerated(0)) // AN-GW-FAILED
	m.NewAVP(avpPDNConnectionID, avp.Vbit, vendorID3GPP, datatype.OctetString([]byte{0x01, 0x02, 0x03}))

	wire, err := m.Serialize()
	require.NoError(t, err)
	m, err = diam.ReadMessage(bytes.NewReader(wire), dict.Default)
	require.NoError(t, err)

	event, err := NewTranslator().ToCCREvent(m, "gx-wire")
	require.NoError(t, err)
	assert.Equal(t, BearerOperationModification, event.BearerOperation)
	assert.Equal(t, uint32(0), event.ANGWStatus)
	assert.Equal(t, []byte{0x01, 0x02, 0x03}, event.PDNConnectionID, "OctetString, TS 29.212 §5.3.58")
}
