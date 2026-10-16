// Copyright 2026 3eAI Labs Ltd
// SPDX-License-Identifier: AGPL-3.0-only

package diameter

import (
	"bytes"
	"testing"

	"github.com/fiorix/go-diameter/v4/diam"
	"github.com/fiorix/go-diameter/v4/diam/dict"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// ulaHeader is a Diameter header (RFC 6733 §3) for an Update-Location-Answer:
// version 1, flags 0 (answer), command 316, application 16777251 (S6a). The
// three length octets are filled in by withLength.
var ulaHeader = []byte{
	0x01, 0x00, 0x00, 0x00, // version, message length
	0x00, 0x00, 0x01, 0x3c, // flags, command code 316
	0x01, 0x00, 0x00, 0x23, // application 16777251
	0x00, 0x00, 0x00, 0x01, // hop-by-hop
	0x00, 0x00, 0x00, 0x02, // end-to-end
}

func withLength(avps ...[]byte) []byte {
	m := append([]byte{}, ulaHeader...)
	for _, a := range avps {
		m = append(m, a...)
	}
	m[1], m[2], m[3] = byte(len(m)>>16), byte(len(m)>>8), byte(len(m))
	return m
}

// Octets written from RFC 6733 §4.1 (AVP header: code, flags, 3-octet length)
// and §7.1, §7.6, §7.7 — not produced by the gateway's encoder.
var (
	resultCode2001 = []byte{
		0x00, 0x00, 0x01, 0x0c, 0x40, 0x00, 0x00, 0x0c, // Result-Code (268), M, length 12
		0x00, 0x00, 0x07, 0xd1, // 2001
	}
	experimentalResult5001 = []byte{
		0x00, 0x00, 0x01, 0x29, 0x40, 0x00, 0x00, 0x20, // Experimental-Result (297), M, length 32
		0x00, 0x00, 0x01, 0x0a, 0x40, 0x00, 0x00, 0x0c, // Vendor-Id (266), M, length 12
		0x00, 0x00, 0x28, 0xaf, // 10415 (3GPP)
		0x00, 0x00, 0x01, 0x2a, 0x40, 0x00, 0x00, 0x0c, // Experimental-Result-Code (298), M, length 12
		0x00, 0x00, 0x13, 0x89, // 5001 DIAMETER_ERROR_USER_UNKNOWN
	}
)

func decode(t *testing.T, raw []byte) *diam.Message {
	t.Helper()
	m, err := diam.ReadMessage(bytes.NewReader(raw), dict.Default)
	require.NoError(t, err)
	return m
}

func TestAnswerResult_ResultCode(t *testing.T) {
	rc, exp := AnswerResult(decode(t, withLength(resultCode2001)))
	assert.Equal(t, uint32(2001), rc)
	assert.Zero(t, exp)
}

// TestAnswerResult_ExperimentalResult is the TS 29.272 §7.4.3 case: the MME
// reports an S6a error in Experimental-Result and the Result-Code is absent.
// It used to be reported to the application as 2001.
func TestAnswerResult_ExperimentalResult(t *testing.T) {
	rc, exp := AnswerResult(decode(t, withLength(experimentalResult5001)))
	assert.Zero(t, rc)
	assert.Equal(t, uint32(5001), exp)
}

func TestAnswerResult_NeitherIsNotSuccess(t *testing.T) {
	rc, exp := AnswerResult(decode(t, withLength()))
	assert.Equal(t, UnableToComply, rc)
	assert.Zero(t, exp)
}
