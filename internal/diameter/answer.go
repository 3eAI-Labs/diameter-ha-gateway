// Copyright 2026 3eAI Labs Ltd
// SPDX-License-Identifier: AGPL-3.0-only

package diameter

import (
	"strconv"

	"github.com/3eAI-Labs/diameter-ha-gateway/internal/platform/logger"
	"github.com/3eAI-Labs/diameter-ha-gateway/internal/platform/monitor"
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
// identity; the request AVPs in echoedAVPs; and every Proxy-Info of the
// request, in order (§6.2). AVPs already on the answer are kept, so an
// application may answer with its own Origin-Host.
//
// It also fixes the header: the T flag is cleared (§3: it MUST NOT be set in
// answers, and go-diameter's Answer copies it from the request), and the E
// flag is set on a protocol error, Result-Code 3xxx (§7.1.3). An answer that
// carries neither Result-Code nor Experimental-Result — an application reply
// without a result — is answered 5012 (DIAMETER_UNABLE_TO_COMPLY).
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
	if !hasAVP(ans, avp.ProxyInfo) {
		for _, a := range req.AVP {
			if a.Code == avp.ProxyInfo && a.VendorID == 0 {
				ans.AddAVP(a)
			}
		}
	}
	if !hasAVP(ans, avp.ResultCode) && !hasAVP(ans, avp.ExperimentalResult) {
		logger.Warn("Answer carries no result; answering 5012",
			logger.String("command_code", strconv.FormatUint(uint64(ans.Header.CommandCode), 10)))
		monitor.IncrementCounter("diameter_answer_without_result_total",
			map[string]string{"command_code": strconv.FormatUint(uint64(ans.Header.CommandCode), 10)})
		ans.NewAVP(avp.ResultCode, avp.Mbit, 0, datatype.Unsigned32(diam.UnableToComply))
	}
	ans.Header.CommandFlags &^= diam.RetransmittedFlag
	if rc := resultCode(ans); rc >= 3000 && rc < 4000 {
		ans.Header.CommandFlags |= diam.ErrorFlag
	}
	return ans
}

// resultCode returns ans's top-level Result-Code, or 0 when it has none.
func resultCode(ans *diam.Message) uint32 {
	if a := topLevelAVP(ans, avp.ResultCode); a != nil {
		if v, ok := a.Data.(datatype.Unsigned32); ok {
			return uint32(v)
		}
	}
	return 0
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
