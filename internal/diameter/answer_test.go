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
