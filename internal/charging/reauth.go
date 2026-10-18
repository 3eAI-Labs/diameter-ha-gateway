// Copyright 2026 3eAI Labs Ltd
// SPDX-License-Identifier: AGPL-3.0-only

package charging

import (
	"github.com/fiorix/go-diameter/v4/diam"
	"github.com/fiorix/go-diameter/v4/diam/avp"
	"github.com/fiorix/go-diameter/v4/diam/datatype"
)

// The requests an OCS sends a Gy or Ro client through the gateway:
// Re-Auth-Request (TS 32.299 §6.4.4, RFC 8506 §3.4) and Abort-Session-
// Request (TS 32.299 §6.4.12, RFC 6733 §8.5.1), both under Application-Id 4.

const (
	CommandRAR = 258
	CommandASR = 274

	codeReAuthRequestType = 285
	codeCCSubSessionID    = 419
)

// SessionBinder keeps which peer a credit-control session runs on, so a
// RAR or ASR for it goes back over that peer's connection.
type SessionBinder interface {
	Bind(sessionID, peerHost string)
	Unbind(sessionID, peerHost string)
}

// TrackSession binds the session of a CCR to peerHost on INITIAL and
// UPDATE, and unbinds it on TERMINATION. A nil binder does nothing.
func TrackSession(b SessionBinder, ccRequestType uint32, sessionID, peerHost string) {
	if b == nil || sessionID == "" || peerHost == "" {
		return
	}
	switch ccRequestType {
	case CCRequestTypeInitial, CCRequestTypeUpdate:
		b.Bind(sessionID, peerHost)
	case CCRequestTypeTermination:
		b.Unbind(sessionID, peerHost)
	}
}

// ReAuthRequest asks the gateway to send a RAR for a credit-control
// session. Header.SessionID names the session; Header.DestinationHost, or
// else Header.OriginHost as echoed from the CCR, names the client when the
// gateway has no binding for the session. Re-Auth-Request-Type
// AUTHORIZE_ONLY (0) is the default.
type ReAuthRequest struct {
	Header            DiameterHeader `json:"header"`
	ReAuthRequestType uint32         `json:"re_auth_request_type"`
	UserName          string         `json:"user_name,omitempty"`
	CCSubSessionID    *uint64        `json:"cc_sub_session_id,omitempty"`
	GSUPoolIdentifier *uint32        `json:"gsu_pool_identifier,omitempty"`
	ServiceIdentifier *uint32        `json:"service_identifier,omitempty"`
	RatingGroup       *uint32        `json:"rating_group,omitempty"`
}

// AbortSessionRequest asks the gateway to send an ASR (RFC 6733 §8.5.1).
type AbortSessionRequest struct {
	Header   DiameterHeader `json:"header"`
	UserName string         `json:"user_name,omitempty"`
}

// AbortAnswer is the reply to a ReAuthRequest or AbortSessionRequest: the
// client's result, the Experimental-Result-Code in ResultCode when it sent
// one, its Error-Message, its identity and the RAA's identifiers (TS 32.299
// §6.4.5: not used in 3GPP, passed on when present).
type AbortAnswer struct {
	ResultCode             uint32  `json:"result_code"`
	ExperimentalResultCode uint32  `json:"experimental_result_code,omitempty"`
	ErrorMessage           string  `json:"error_message,omitempty"`
	OriginHost             string  `json:"origin_host,omitempty"`
	OriginRealm            string  `json:"origin_realm,omitempty"`
	CCSubSessionID         *uint64 `json:"cc_sub_session_id,omitempty"`
	GSUPoolIdentifier      *uint32 `json:"gsu_pool_identifier,omitempty"`
	ServiceIdentifier      *uint32 `json:"service_identifier,omitempty"`
	RatingGroup            *uint32 `json:"rating_group,omitempty"`
}

// requestHeader opens a RAR or ASR: R and P flags ("REQ, PXY"), Session-Id,
// origin, destination and Auth-Application-Id 4 (TS 32.299 §6.4.4, RFC 6733
// §8.5.1).
func requestHeader(code uint32, sessionID, destHost, destRealm, originHost, originRealm string) *diam.Message {
	m := diam.NewMessage(code, diam.RequestFlag|diam.ProxiableFlag, AppIDCreditControl, 0, 0, nil)
	for _, a := range []*diam.AVP{
		diam.NewAVP(avp.SessionID, avp.Mbit, 0, datatype.UTF8String(sessionID)),
		diam.NewAVP(avp.OriginHost, avp.Mbit, 0, datatype.DiameterIdentity(originHost)),
		diam.NewAVP(avp.OriginRealm, avp.Mbit, 0, datatype.DiameterIdentity(originRealm)),
		diam.NewAVP(avp.DestinationRealm, avp.Mbit, 0, datatype.DiameterIdentity(destRealm)),
		diam.NewAVP(avp.DestinationHost, avp.Mbit, 0, datatype.DiameterIdentity(destHost)),
		diam.NewAVP(avp.AuthApplicationID, avp.Mbit, 0, datatype.Unsigned32(AppIDCreditControl)),
	} {
		m.AddAVP(a)
	}
	return m
}

func userName(m *diam.Message, name string) {
	if name != "" {
		m.AddAVP(diam.NewAVP(avp.UserName, avp.Mbit, 0, datatype.UTF8String(name)))
	}
}

// BuildRAR is a Gy/Ro Re-Auth-Request (TS 32.299 §6.4.4).
func BuildRAR(r *ReAuthRequest, destHost, destRealm, originHost, originRealm string) *diam.Message {
	m := requestHeader(CommandRAR, r.Header.SessionID, destHost, destRealm, originHost, originRealm)
	m.AddAVP(cc(codeReAuthRequestType, datatype.Enumerated(r.ReAuthRequestType)))
	userName(m, r.UserName)
	if r.CCSubSessionID != nil {
		m.AddAVP(cc(codeCCSubSessionID, datatype.Unsigned64(*r.CCSubSessionID)))
	}
	for _, a := range []*diam.AVP{
		ptrU32(codeGSUPoolIdentifier, r.GSUPoolIdentifier, cc),
		ptrU32(codeServiceIdentifier, r.ServiceIdentifier, cc),
		ptrU32(codeRatingGroup, r.RatingGroup, cc),
	} {
		if a != nil {
			m.AddAVP(a)
		}
	}
	return m
}

// BuildASR is a Gy/Ro Abort-Session-Request (RFC 6733 §8.5.1).
func BuildASR(r *AbortSessionRequest, destHost, destRealm, originHost, originRealm string) *diam.Message {
	m := requestHeader(CommandASR, r.Header.SessionID, destHost, destRealm, originHost, originRealm)
	userName(m, r.UserName)
	return m
}

// ReadAbortAnswer reads a RAA or ASA for the application.
func ReadAbortAnswer(m *diam.Message, resultCode, experimentalResultCode uint32) AbortAnswer {
	out := AbortAnswer{ResultCode: resultCode, ExperimentalResultCode: experimentalResultCode}
	if experimentalResultCode != 0 {
		out.ResultCode = experimentalResultCode
	}
	out.ErrorMessage = text(pick(m.AVP, avp.ErrorMessage, 0))
	out.OriginHost = text(pick(m.AVP, avp.OriginHost, 0))
	out.OriginRealm = text(pick(m.AVP, avp.OriginRealm, 0))
	if a := pick(m.AVP, codeCCSubSessionID, 0); a != nil {
		v := u64(a)
		out.CCSubSessionID = &v
	}
	out.GSUPoolIdentifier = u32p(pick(m.AVP, codeGSUPoolIdentifier, 0))
	out.ServiceIdentifier = u32p(pick(m.AVP, codeServiceIdentifier, 0))
	out.RatingGroup = u32p(pick(m.AVP, codeRatingGroup, 0))
	return out
}
