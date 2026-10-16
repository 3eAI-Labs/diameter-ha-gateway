// Copyright 2026 3eAI Labs Ltd
// SPDX-License-Identifier: AGPL-3.0-only

package diameter

import (
	"github.com/fiorix/go-diameter/v4/diam"
	"github.com/fiorix/go-diameter/v4/diam/datatype"
)

// AVPString returns the value of an AVP of a string type — UTF8String,
// DiameterIdentity or DiameterURI — and "" for any other type.
//
// Never use a.Data.String() for a value: go-diameter formats it for
// debugging ("UTF8String{286011234567890},Padding:1"). Forwarding that string
// put it in every event's IMSI and header, kept MME answers from matching
// their pending requests, and made every CCR miss its Service-Context-Id.
func AVPString(a *diam.AVP) string {
	switch v := a.Data.(type) {
	case datatype.UTF8String:
		return string(v)
	case datatype.DiameterIdentity:
		return string(v)
	case datatype.DiameterURI:
		return string(v)
	}
	return ""
}
