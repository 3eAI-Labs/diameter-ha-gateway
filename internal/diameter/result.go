// Copyright 2026 3eAI Labs Ltd
// SPDX-License-Identifier: AGPL-3.0-only

package diameter

import (
	"github.com/fiorix/go-diameter/v4/diam"
	"github.com/fiorix/go-diameter/v4/diam/avp"
	"github.com/fiorix/go-diameter/v4/diam/datatype"
)

// UnableToComply is DIAMETER_UNABLE_TO_COMPLY (RFC 6733 §7.1.5).
const UnableToComply uint32 = 5012

// AnswerResult returns the outcome an answer carries. A peer reports base
// protocol results in Result-Code, and application results such as
// DIAMETER_ERROR_USER_UNKNOWN (5001) in Experimental-Result with the
// Result-Code absent (RFC 6733 §7.6, TS 29.272 §7.4.3). Exactly one of the
// two return values is non-zero.
//
// An answer that carries neither is malformed (RFC 6733 §7.6). It is reported
// as UnableToComply rather than success, so that the application never reads
// a missing result as 2001.
func AnswerResult(m *diam.Message) (resultCode, experimentalResultCode uint32) {
	if a, err := m.FindAVP(avp.ResultCode, 0); err == nil {
		if v, ok := a.Data.(datatype.Unsigned32); ok {
			return uint32(v), 0
		}
	}
	for _, a := range m.AVP {
		if a.Code != avp.ExperimentalResult {
			continue
		}
		g, ok := a.Data.(*diam.GroupedAVP)
		if !ok {
			continue
		}
		for _, c := range g.AVP {
			if c.Code != avp.ExperimentalResultCode {
				continue
			}
			if v, ok := c.Data.(datatype.Unsigned32); ok && v != 0 {
				return 0, uint32(v)
			}
		}
	}
	return UnableToComply, 0
}
