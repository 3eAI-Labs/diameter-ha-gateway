// Copyright 2026 3eAI Labs Ltd
// SPDX-License-Identifier: AGPL-3.0-only

package diameter

import (
	"github.com/fiorix/go-diameter/v4/diam"
	"github.com/fiorix/go-diameter/v4/diam/avp"
	"github.com/fiorix/go-diameter/v4/diam/datatype"
)

// echoedAVPs are copied from the request when the answer lacks them: the
// Session-Id (RFC 6733 §8.8), the Auth-Session-State the S6a, S13 and SGd
// answer ABNF require, and the Auth-Application-Id, CC-Request-Type and
// CC-Request-Number a Credit-Control-Answer requires (RFC 4006 §3.2).
var echoedAVPs = []uint32{
	avp.AuthSessionState,
	avp.AuthApplicationID,
	avp.CCRequestType,
	avp.CCRequestNumber,
}

// CompleteAnswer adds to ans what every answer to req must carry and the
// application's reply does not supply: the request's Session-Id, as the first
// AVP; Origin-Host and Origin-Realm (RFC 6733 §6.3, §6.4), with the gateway's
// identity; and the request AVPs in echoedAVPs. AVPs already on the answer
// are kept, so an application may answer with its own Origin-Host.
func CompleteAnswer(ans, req *diam.Message, originHost, originRealm string) *diam.Message {
	if !hasAVP(ans, avp.SessionID) {
		if a := topLevelAVP(req, avp.SessionID); a != nil {
			ans.InsertAVP(diam.NewAVP(a.Code, a.Flags, a.VendorID, a.Data))
		}
	}
	if !hasAVP(ans, avp.OriginHost) && originHost != "" {
		ans.NewAVP(avp.OriginHost, avp.Mbit, 0, datatype.DiameterIdentity(originHost))
	}
	if !hasAVP(ans, avp.OriginRealm) && originRealm != "" {
		ans.NewAVP(avp.OriginRealm, avp.Mbit, 0, datatype.DiameterIdentity(originRealm))
	}
	for _, code := range echoedAVPs {
		if hasAVP(ans, code) {
			continue
		}
		if a := topLevelAVP(req, code); a != nil {
			ans.NewAVP(a.Code, a.Flags, a.VendorID, a.Data)
		}
	}
	return ans
}

// SendAnswer completes ans with the identity pm carries and writes it to c.
func SendAnswer(c diam.Conn, ans, req *diam.Message, pm *PeerManager) error {
	host, realm := pm.Identity()
	_, err := CompleteAnswer(ans, req, host, realm).WriteTo(c)
	return err
}

func hasAVP(m *diam.Message, code uint32) bool { return topLevelAVP(m, code) != nil }

// topLevelAVP finds a base-protocol (vendor 0) AVP among m's top-level AVPs.
func topLevelAVP(m *diam.Message, code uint32) *diam.AVP {
	for _, a := range m.AVP {
		if a.Code == code && a.VendorID == 0 {
			return a
		}
	}
	return nil
}
