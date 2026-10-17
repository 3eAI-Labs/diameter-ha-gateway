// Copyright 2026 3eAI Labs Ltd
// SPDX-License-Identifier: AGPL-3.0-only

package diameter

import (
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net"
	"time"

	pb "github.com/3eAI-Labs/diameter-ha-gateway-client"
	"github.com/3eAI-Labs/diameter-ha-gateway/internal/platform/monitor"
	"github.com/fiorix/go-diameter/v4/diam"
	"github.com/fiorix/go-diameter/v4/diam/avp"
	"github.com/fiorix/go-diameter/v4/diam/datatype"
)

// Translator builds Diameter request/answer messages from internal events.
// originStateID is captured once at construction (gateway start time) so
// peers can use Origin-State-Id (RFC 6733 §8.16) to detect gateway
// restarts. Recomputing it per call made peers see the value change
// every second and treat each DWR as a fresh restart, defeating the entire purpose of §8.16.
type Translator struct {
	originStateID uint32
}

func NewTranslator() *Translator {
	return &Translator{
		originStateID: uint32(time.Now().Unix()),
	}
}

// findAVP searches for an AVP by code and vendor ID manually.
// This is needed because go-diameter's FindAVP requires AVPs to be in the dictionary,
// but many 3GPP vendor-specific AVPs are not registered.
func (t *Translator) findAVP(m *diam.Message, code uint32, vendorID uint32) *diam.AVP {
	for _, a := range m.AVP {
		if a.Code == code && a.VendorID == vendorID {
			return a
		}
	}
	return nil
}

func (t *Translator) extractHeader(m *diam.Message, traceID string) pb.DiameterHeader {
	var sourceRealm, destRealm, sessionID, originHost string
	if a, err := m.FindAVP(avp.OriginRealm, 0); err == nil {
		sourceRealm = AVPString(a)
	}
	if a, err := m.FindAVP(avp.DestinationRealm, 0); err == nil {
		destRealm = AVPString(a)
	}
	if a, err := m.FindAVP(avp.SessionID, 0); err == nil {
		sessionID = AVPString(a)
	}
	if a, err := m.FindAVP(avp.OriginHost, 0); err == nil {
		originHost = AVPString(a)
	}

	return pb.DiameterHeader{
		TraceID:     traceID,
		SourceHost:  originHost,
		SourceRealm: sourceRealm,
		DestRealm:   destRealm,
		Timestamp:   time.Now(),
		HopByHop:    m.Header.HopByHopID,
		EndToEnd:    m.Header.EndToEndID,
		SessionID:   sessionID,
	}
}

func (t *Translator) ToS6aEvent(m *diam.Message, traceID string) (*pb.S6aEvent, error) {
	event := &pb.S6aEvent{
		Header: t.extractHeader(m, traceID),
	}

	if a, err := m.FindAVP(avp.UserName, 0); err == nil {
		event.IMSI = AVPString(a)
	}

	// 3GPP vendor-specific AVPs (vendor ID 10415) need manual lookup
	// because go-diameter's FindAVP requires AVPs to be in dictionary
	if a := t.findAVP(m, avp.RATType, 10415); a != nil {
		event.RATType = uint32(a.Data.(datatype.Enumerated))
	}
	if a := t.findAVP(m, avp.ULRFlags, 10415); a != nil {
		event.ULRFlags = uint32(a.Data.(datatype.Unsigned32))
	}
	if a := t.findAVP(m, avp.VisitedPLMNID, 10415); a != nil {
		event.VisitedPLMN = fmt.Sprintf("%x", a.Data.Serialize())
	}
	if a := t.findAVP(m, avp.SGSNNumber, 10415); a != nil {
		event.SGSNNumber = fmt.Sprintf("%x", a.Data.Serialize())
	}
	if a := t.findAVP(m, avp.UESRVCCCapability, 10415); a != nil {
		event.UE_SRVCC = uint32(a.Data.(datatype.Enumerated)) // Enumerated, TS 29.272 §7.3.130
	}
	if a := t.findAVP(m, avp.HomogeneousSupportofIMSVoiceOverPSSessions, 10415); a != nil {
		event.HomogeneousIMS = uint32(a.Data.(datatype.Enumerated))
	}
	if a := t.findAVP(m, avp.GMLCAddress, 10415); a != nil {
		if ip, ok := a.Data.(datatype.Address); ok {
			event.GMLCAddress = net.IP(ip).String()
		}
	}

	if a := t.findAVP(m, avp.TerminalInformation, 10415); a != nil {
		event.TerminalInfo = t.parseTerminalInfo(a)
	}

	if a := t.findAVP(m, avp.RequestedEUTRANAuthenticationInfo, 10415); a != nil {
		event.AuthInfo = t.parseAuthInfo(a)
	}

	// Converged-HSS 2G/3G auth (Requested-UTRAN-GERAN-Authentication-
	// Info, AVP 1409). Same Grouped child structure as EUTRAN — reuse
	// parseAuthInfo. MME populates one or both depending on the access
	// technology the UE is attaching from (LTE vs UMTS/GERAN).
	if a := t.findAVP(m, avp.RequestedUTRANGERANAuthenticationInfo, 10415); a != nil {
		event.AuthInfoUTRAN = t.parseAuthInfo(a)
	}

	if a := t.findAVP(m, avp.PURFlags, 10415); a != nil {
		event.PURFlags = uint32(a.Data.(datatype.Unsigned32))
	}

	if a := t.findAVP(m, avp.NORFlags, 10415); a != nil {
		event.NORFlags = uint32(a.Data.(datatype.Unsigned32))
	}
	if a := t.findAVP(m, avp.ContextIdentifier, 10415); a != nil {
		event.ContextID = uint32(a.Data.(datatype.Unsigned32))
	}
	if a, err := m.FindAVP(avp.ServiceSelection, 0); err == nil {
		event.ServiceSelect = AVPString(a)
	}
	if a := t.findAVP(m, 1434, 10415); a != nil { // Alert-Reason, Enumerated (§7.3.83)
		event.AlertReason = uint32(a.Data.(datatype.Enumerated))
	}

	// Active-APN (1612, TS 29.272 §7.3.127) — list of currently-
	// active APNs the UE has, sent by MME so HSS can scope APN-Configuration
	// download decisions. Repeated AVP, each carries Service-Selection (493)
	// child with the APN name.
	for _, a := range m.AVP {
		if a.Code != avp.ActiveAPN {
			continue
		}
		g, ok := a.Data.(*diam.GroupedAVP)
		if !ok {
			continue
		}
		for _, sub := range g.AVP {
			if sub.Code == avp.ServiceSelection {
				// UTF8String.String() returns the go-diameter debug format
				// `UTF8String{...},Padding:N` — not suitable for downstream
				// consumers. Type-assert directly. See reverse.go:
				// extractSessionID for the same pattern.
				if s, ok := sub.Data.(datatype.UTF8String); ok {
					event.ActiveAPN = append(event.ActiveAPN, string(s))
				}
			}
		}
	}

	// Equivalent-PLMN-List (1637, TS 29.272 §7.3.151) is Grouped:
	// 1*{ Visited-PLMN-Id }, each a 3-octet TBCD PLMN-Id. go-diameter v4 has
	// no constant for 1637.
	if a := t.findAVP(m, 1637, 10415); a != nil {
		if g, ok := a.Data.(*diam.GroupedAVP); ok {
			for _, sub := range g.AVP {
				if sub.Code == avp.VisitedPLMNID && sub.VendorID == 10415 {
					event.EquivalentPLMNList = append(event.EquivalentPLMNList,
						fmt.Sprintf("%x", sub.Data.Serialize()))
				}
			}
		}
	}

	// MME-Number-for-MT-SMS (1645, TS 29.272 §7.3.158). Single
	// OctetString with E.164 number TBCD-encoded — used for SGd MT routing.
	if a := t.findAVP(m, avp.MMENumberforMTSMS, 10415); a != nil {
		event.MMENumberForMTSMS = fmt.Sprintf("%x", a.Data.Serialize())
	}

	// Trace-Data full decode (TS 29.272
	// §7.3.95-104). Backward-compat: producers that send only TraceReference
	// continue to populate the slim path (other fields zero-value); the
	// expanded decode also picks up TraceDepth, NE/Interface/Event lists
	// (opaque bitstrings per TS 32.422 §5.1), OMC-Id, TraceCollectionEntity
	// (Address → IP string), and nested MDTConfiguration.
	if a := t.findAVP(m, avp.TraceData, 10415); a != nil {
		if g, ok := a.Data.(*diam.GroupedAVP); ok {
			td := decodeTraceData(g)
			if td != nil {
				event.TraceData = td
			}
		}
	}

	// DRMP (301, RFC 7944) propagation. When MME marks the request
	// with a routing priority, gateway captures it so backend can decide
	// whether to echo it back on the answer (proxy-preserve pattern).
	if a, err := m.FindAVP(avp.DRMP, 0); err == nil {
		if e, ok := a.Data.(datatype.Enumerated); ok {
			event.DRMP = uint32(e)
		}
	}

	// Supported-Features (628) — TS 29.229 §6.2 / TS 29.272 §7.3.10. Repeated
	// Grouped AVP advertising MME-supported feature bits (Multiple-PDN-
	// Connectivity, ARP-Override, Mission-Critical-Service, etc.). HSS uses
	// this to gate Rel-12+ behavior. Each instance carries Vendor-Id (266,
	// base) + Feature-List-ID (629, vendor 10415) + Feature-List (630, vendor
	// 10415). Forward the parsed list to the backend verbatim — gateway
	// makes no judgment about which features to honor.
	for _, a := range m.AVP {
		if a.Code != avp.SupportedFeatures {
			continue
		}
		g, ok := a.Data.(*diam.GroupedAVP)
		if !ok {
			continue
		}
		sf := pb.SupportedFeature{}
		for _, sub := range g.AVP {
			switch sub.Code {
			case avp.VendorID: // base IETF code 266
				if v, ok := sub.Data.(datatype.Unsigned32); ok {
					sf.VendorID = uint32(v)
				}
			case avp.FeatureListID: // vendor 10415, code 629
				if v, ok := sub.Data.(datatype.Unsigned32); ok {
					sf.FeatureListID = uint32(v)
				}
			case avp.FeatureList: // vendor 10415, code 630
				if v, ok := sub.Data.(datatype.Unsigned32); ok {
					sf.FeatureList = uint32(v)
				}
			}
		}
		event.SupportedFeatures = append(event.SupportedFeatures, sf)
	}

	return event, nil
}

func (t *Translator) ToS13Event(m *diam.Message, traceID string) (*pb.S13Event, error) {
	event := &pb.S13Event{
		Header: t.extractHeader(m, traceID),
	}

	if a, err := m.FindAVP(avp.UserName, 0); err == nil {
		event.IMSI = AVPString(a)
	}

	// 3GPP vendor-specific AVPs need manual lookup
	if a := t.findAVP(m, avp.TerminalInformation, 10415); a != nil {
		event.TerminalInfo = t.parseTerminalInfo(a)
	}

	return event, nil
}

func (t *Translator) parseTerminalInfo(a *diam.AVP) *pb.TerminalInfo {
	info := &pb.TerminalInfo{}
	if g, ok := a.Data.(*diam.GroupedAVP); ok {
		for _, sub := range g.AVP {
			switch sub.Code {
			case avp.IMEI:
				info.IMEI = AVPString(sub)
			case avp.SoftwareVersion:
				info.SV = AVPString(sub)
			}
		}
	}
	return info
}

func (t *Translator) parseAuthInfo(a *diam.AVP) *pb.AuthInfo {
	info := &pb.AuthInfo{}
	if g, ok := a.Data.(*diam.GroupedAVP); ok {
		for _, sub := range g.AVP {
			switch sub.Code {
			case avp.NumberOfRequestedVectors:
				info.RequestedVectors = uint32(sub.Data.(datatype.Unsigned32))
			case avp.ImmediateResponsePreferred:
				info.ImmediateRes = sub.Data.(datatype.Unsigned32) == 1
			case avp.ResynchronizationInfo:
				info.ReSyncInfo = fmt.Sprintf("%x", sub.Data.Serialize())
			}
		}
	}
	return info
}

func (t *Translator) MaskIMSI(imsi string) string {
	if len(imsi) < 8 {
		return "****"
	}
	return imsi[:6] + "******" + imsi[len(imsi)-2:]
}

func (t *Translator) CreateErrorAnswer(m *diam.Message, resultCode uint32) (*diam.Message, error) {
	return m.Answer(resultCode), nil
}

// uniqueSessionID builds a Session-Id that is unique even when the same IMSI
// is targeted by multiple in-flight reverse requests. Format: {imsi}-{kind}-{nano}.
// A plain {imsi}-cl would collide when two CLRs for the same subscriber overlap.
func uniqueSessionID(imsi, kind string) string {
	return fmt.Sprintf("%s-%s-%d", imsi, kind, time.Now().UnixNano())
}

// CreateCLR builds a Cancel-Location-Request (TS 29.272 §6.1.1).
// Returns the message and the generated Session-Id so the caller can register
// in pendingReqs without re-extracting the AVP.
func (t *Translator) CreateCLR(imsi, destHost, destRealm, originHost, originRealm string) (*diam.Message, string) {
	sid := uniqueSessionID(imsi, "cl")
	m := diam.NewRequest(317, 16777251, nil)
	m.NewAVP(avp.SessionID, avp.Mbit, 0, datatype.UTF8String(sid))
	m.NewAVP(avp.OriginHost, avp.Mbit, 0, datatype.DiameterIdentity(originHost))
	m.NewAVP(avp.OriginRealm, avp.Mbit, 0, datatype.DiameterIdentity(originRealm))
	m.NewAVP(avp.DestinationHost, avp.Mbit, 0, datatype.DiameterIdentity(destHost))
	m.NewAVP(avp.DestinationRealm, avp.Mbit, 0, datatype.DiameterIdentity(destRealm))
	m.NewAVP(avp.UserName, avp.Mbit, 0, datatype.UTF8String(imsi))
	m.NewAVP(avp.AuthSessionState, avp.Mbit, 0, datatype.Enumerated(1)) // NO_STATE_MAINTAINED
	m.AddAVP(avp3GPP(avp.CancellationType, datatype.Enumerated(0)))
	return m, sid
}

// CreateDWR builds a Device-Watchdog-Request (RFC 6733 §5.5.1).
// Application-Id is 0 (Common Diameter Application). Origin-State-Id
// (RFC 6733 §8.16) is sourced from the translator's stable per-process
// value so peers can detect gateway restarts but not see spurious
// transitions on every probe. No Session-Id (DWR is connection-scoped).
func (t *Translator) CreateDWR(originHost, originRealm string) *diam.Message {
	m := diam.NewRequest(280, 0, nil)
	m.NewAVP(avp.OriginHost, avp.Mbit, 0, datatype.DiameterIdentity(originHost))
	m.NewAVP(avp.OriginRealm, avp.Mbit, 0, datatype.DiameterIdentity(originRealm))
	m.NewAVP(avp.OriginStateID, avp.Mbit, 0, datatype.Unsigned32(t.originStateID))
	return m
}

// OriginStateID exposes the translator's per-process Origin-State-Id value
// so other call sites (e.g. a future custom DWR handler) can keep the same
// epoch across all gateway-emitted messages.
func (t *Translator) OriginStateID() uint32 {
	return t.originStateID
}

// CreateDPR builds a Disconnect-Peer-Request (RFC 6733 §5.4.3) with the
// supplied disconnect cause. Caller picks the cause — REBOOTING (0) is the
// right default for graceful shutdown in a rolling restart, BUSY (1) for
// admin-driven drain, DO_NOT_WANT_TO_TALK_TO_YOU (2) for permanent removal.
func (t *Translator) CreateDPR(originHost, originRealm string, disconnectCause uint32) *diam.Message {
	m := diam.NewRequest(282, 0, nil)
	m.NewAVP(avp.OriginHost, avp.Mbit, 0, datatype.DiameterIdentity(originHost))
	m.NewAVP(avp.OriginRealm, avp.Mbit, 0, datatype.DiameterIdentity(originRealm))
	m.NewAVP(avp.DisconnectCause, avp.Mbit, 0, datatype.Enumerated(int32(disconnectCause)))
	return m
}

// CreateDPA builds a Disconnect-Peer-Answer in response to an inbound DPR
// (RFC 6733 §5.4.4). The H2H/E2E IDs and command code are preserved by
// diam.Message.Answer so peers can correlate their pending DPR.
//
// Required by handleDPR — go-diameter's sm package only auto-replies to
// CER and DWR, NOT to DPR. Without this answer, peers wait until their
// own DPR timeout and treat the disconnect as ungraceful (RFC §5.4.3).
func (t *Translator) CreateDPA(req *diam.Message, originHost, originRealm string) *diam.Message {
	a := req.Answer(diam.Success)
	a.NewAVP(avp.OriginHost, avp.Mbit, 0, datatype.DiameterIdentity(originHost))
	a.NewAVP(avp.OriginRealm, avp.Mbit, 0, datatype.DiameterIdentity(originRealm))
	return a
}

// CreateRSR builds a Reset-Request (TS 29.272 §7.2.15), HSS→MME. The RSR
// scopes the reset with User-Id (§7.3.50), the leading digits of an IMSI: it
// selects the subscribers whose IMSI begins with them, so a full IMSI selects
// one. With `imsiPrefix` empty the RSR carries no User-Id and covers every
// subscriber the MME holds from this HSS. The RSR has no User-Name.
func (t *Translator) CreateRSR(imsiPrefix, destHost, destRealm, originHost, originRealm string) (*diam.Message, string) {
	keyForSid := imsiPrefix
	if keyForSid == "" {
		keyForSid = "broadcast"
	}
	sid := uniqueSessionID(keyForSid, "rs")
	m := diam.NewRequest(322, 16777251, nil)
	m.NewAVP(avp.SessionID, avp.Mbit, 0, datatype.UTF8String(sid))
	m.NewAVP(avp.OriginHost, avp.Mbit, 0, datatype.DiameterIdentity(originHost))
	m.NewAVP(avp.OriginRealm, avp.Mbit, 0, datatype.DiameterIdentity(originRealm))
	m.NewAVP(avp.DestinationHost, avp.Mbit, 0, datatype.DiameterIdentity(destHost))
	m.NewAVP(avp.DestinationRealm, avp.Mbit, 0, datatype.DiameterIdentity(destRealm))
	m.NewAVP(avp.AuthSessionState, avp.Mbit, 0, datatype.Enumerated(1))
	if imsiPrefix != "" {
		m.AddAVP(avp3GPP(avpUserID, datatype.UTF8String(imsiPrefix)))
	}
	return m, sid
}

// CreateDSR builds a Delete-Subscriber-Data-Request (TS 29.272 §6.1.4).
// DSR-Flags is mandatory per spec — callers SHOULD pass non-zero flags or
// the message has no effect (peers may ignore or 5012 it). contextIDs scopes
// APN withdrawal to a specific subset; empty slice means "rely on flags
// alone" (e.g. CompleteAPNConfigurationProfileWithdrawal bit removes all).
func (t *Translator) CreateDSR(imsi, destHost, destRealm, originHost, originRealm string,
	dsrFlags uint32, contextIDs []uint32) (*diam.Message, string) {
	sid := uniqueSessionID(imsi, "ds")
	m := diam.NewRequest(320, 16777251, nil)
	m.NewAVP(avp.SessionID, avp.Mbit, 0, datatype.UTF8String(sid))
	m.NewAVP(avp.OriginHost, avp.Mbit, 0, datatype.DiameterIdentity(originHost))
	m.NewAVP(avp.OriginRealm, avp.Mbit, 0, datatype.DiameterIdentity(originRealm))
	m.NewAVP(avp.DestinationHost, avp.Mbit, 0, datatype.DiameterIdentity(destHost))
	m.NewAVP(avp.DestinationRealm, avp.Mbit, 0, datatype.DiameterIdentity(destRealm))
	m.NewAVP(avp.UserName, avp.Mbit, 0, datatype.UTF8String(imsi))
	m.NewAVP(avp.AuthSessionState, avp.Mbit, 0, datatype.Enumerated(1))
	// DSR-Flags (1421, TS 29.272 §7.3.25), mandatory per the DSR ABNF;
	// go-diameter v4 has no constant for it. 1422 is DSA-Flags.
	m.AddAVP(avp3GPP(1421, datatype.Unsigned32(dsrFlags)))
	for _, cid := range contextIDs {
		m.AddAVP(avp3GPP(avp.ContextIdentifier, datatype.Unsigned32(cid)))
	}
	return m, sid
}

// CreateIDR builds an Insert-Subscriber-Data-Request (TS 29.272 §6.1.4).
// Subscription-Data AVP (1400) is encoded as a Grouped tree per TS 29.272
// §7.3.2 from `subProfile`. `subscriberData` is the deprecated legacy
// hex-OctetString path, used only when
// subProfile is nil. idrFlags maps IDR-Flags AVP (bit field
// per TS 29.272 §7.3.103).
func (t *Translator) CreateIDR(imsi, destHost, destRealm, originHost, originRealm string,
	subProfile *pb.SubscriptionProfile, subscriberData string, idrFlags uint32) (*diam.Message, string) {
	sid := uniqueSessionID(imsi, "id")
	m := diam.NewRequest(319, 16777251, nil)
	m.NewAVP(avp.SessionID, avp.Mbit, 0, datatype.UTF8String(sid))
	m.NewAVP(avp.OriginHost, avp.Mbit, 0, datatype.DiameterIdentity(originHost))
	m.NewAVP(avp.OriginRealm, avp.Mbit, 0, datatype.DiameterIdentity(originRealm))
	m.NewAVP(avp.DestinationHost, avp.Mbit, 0, datatype.DiameterIdentity(destHost))
	m.NewAVP(avp.DestinationRealm, avp.Mbit, 0, datatype.DiameterIdentity(destRealm))
	m.NewAVP(avp.UserName, avp.Mbit, 0, datatype.UTF8String(imsi))
	m.NewAVP(avp.AuthSessionState, avp.Mbit, 0, datatype.Enumerated(1))
	emitSubscriptionData(m, subProfile, subscriberData, "IDR")
	if idrFlags != 0 {
		// IDR-Flags AVP code 1490 (vendor 10415) — not exported as a constant
		// in go-diameter v4 so we reference the numeric code directly.
		m.AddAVP(avp3GPP(1490, datatype.Unsigned32(idrFlags)))
	}
	return m, sid
}

// ToDiameterAnswer converts NATS JSON response to Diameter AVPs
func (t *Translator) ToDiameterAnswer(respData []byte, req *diam.Message) (*diam.Message, error) {
	ans := req.Answer(diam.Success)

	switch req.Header.CommandCode {
	case 316: // ULA
		var res pb.S6aAnswer
		if err := json.Unmarshal(respData, &res); err != nil {
			return req.Answer(diam.UnableToDeliver), err
		}
		ans = applyResultCode(req, res.ResultCode, res.ExperimentalResultCode)
		emitSubscriptionData(ans, res.SubProfile, res.SubData, "ULA")
		if res.ULAFlags != 0 {
			ans.AddAVP(avp3GPP(avp.ULAFlags, datatype.Unsigned32(res.ULAFlags)))
		}
		applyRedirectAVPs(ans, &res)
		applySupportedFeatures(ans, res.SupportedFeatures)
		applyResetIDs(ans, res.ResetIDs)
		applyFailedAVPs(ans, res.FailedAVPs)

	case 318: // AIA — TS 29.272 §7.3
		var res pb.S6aAnswer
		if err := json.Unmarshal(respData, &res); err != nil {
			return req.Answer(diam.UnableToDeliver), err
		}
		ans = applyResultCode(req, res.ResultCode, res.ExperimentalResultCode)

		if vectors, err := encodeAuthVectors(res.AuthVectors); err != nil {
			// Hex-decode failure is a backend producer bug — return
			// 5012 so the caller sees a deterministic failure code
			// rather than an answer with corrupt crypto material.
			return req.Answer(5012 /* DIAMETER_UNABLE_TO_COMPLY */), err
		} else if vectors != nil {
			ans.AddAVP(avp3GPP(avp.AuthenticationInfo, vectors))
		}
		applyRedirectAVPs(ans, &res)
		applySupportedFeatures(ans, res.SupportedFeatures)
		applyFailedAVPs(ans, res.FailedAVPs)

	case 321: // PUA — TS 29.272 §7.3.27
		var res pb.S6aAnswer
		if err := json.Unmarshal(respData, &res); err != nil {
			return req.Answer(diam.UnableToDeliver), err
		}
		ans = applyResultCode(req, res.ResultCode, res.ExperimentalResultCode)
		if res.PUAFlags != 0 {
			ans.AddAVP(avp3GPP(avp.PUAFlags, datatype.Unsigned32(res.PUAFlags)))
		}
		applyRedirectAVPs(ans, &res)
		applySupportedFeatures(ans, res.SupportedFeatures)
		applyFailedAVPs(ans, res.FailedAVPs)

	case 323: // NOA — TS 29.272 §7.3.28
		// Result-Code only per spec — no NOA-specific AVPs in the answer.
		// Explicit case (rather than falling through to default) so
		// future readers see NOA was considered, not forgotten.
		var res pb.S6aAnswer
		if err := json.Unmarshal(respData, &res); err != nil {
			return req.Answer(diam.UnableToDeliver), err
		}
		ans = applyResultCode(req, res.ResultCode, res.ExperimentalResultCode)
		applyRedirectAVPs(ans, &res)
		applySupportedFeatures(ans, res.SupportedFeatures)
		applyFailedAVPs(ans, res.FailedAVPs)

	case 324: // ECA — TS 29.272 §7.2.20
		var res pb.S13Answer
		if err := json.Unmarshal(respData, &res); err != nil {
			return req.Answer(diam.UnableToDeliver), err
		}
		ans = applyResultCode(req, res.ResultCode, res.ExperimentalResultCode)
		// Equipment-Status (1445, §7.3.51) only on success: on an error
		// answer its zero value would read as PERMITTEDLISTED.
		if res.ExperimentalResultCode == 0 && res.ResultCode == diam.Success {
			ans.AddAVP(avp3GPP(1445, datatype.Enumerated(res.Status)))
		}

	default:
		var res struct {
			ResultCode uint32 `json:"result_code"`
		}
		if err := json.Unmarshal(respData, &res); err == nil && res.ResultCode != 0 {
			ans = req.Answer(res.ResultCode)
		}
	}

	// RFC 7944 §3.2 DRMP proxy-preserve: copy the request's DRMP
	// AVP onto the answer when present. Gateway acts as a Diameter proxy
	// here, so DRMP MUST survive the request/answer round-trip without
	// backend involvement. Done after the switch so every command (including
	// the default fallback) gets DRMP propagation.
	if a, err := req.FindAVP(avp.DRMP, 0); err == nil {
		if e, ok := a.Data.(datatype.Enumerated); ok {
			ans.NewAVP(avp.DRMP, 0, 0, datatype.Enumerated(e)) // M-bit must not be set: TS 29.272 Table 7.3.1/2
		}
	}

	return ans, nil
}

// SetTBit marks a Diameter request as a retransmission (RFC 6733 §3 +
// §6.1.1, T-bit at Command Flags bit 4). Set this on every request that is
// a duplicate of a previously-sent request — the receiving peer uses it to
// run duplicate detection. Used by the reverse-path redirect retry (the
// second hop after 3006, RFC 6733 §6.5) and any failover-driven retransmit
// (RFC 6733 §5.5.4). No-op when called on an answer message.
func (t *Translator) SetTBit(m *diam.Message) {
	if m == nil {
		return
	}
	m.Header.CommandFlags |= diam.RetransmittedFlag
	monitor.IncrementCounter("diameter_tbit_retransmit_total", map[string]string{
		"cmd": fmt.Sprintf("%d", m.Header.CommandCode),
	})
}

// applyRedirectAVPs encodes the §6.5 redirect indication onto an answer
// message when the response payload requested it. No-op when RedirectHosts
// is empty — keeps non-redirect answers clean. Order of Redirect-Host AVPs
// is preserved: peer policy is "try first, fail to next", so producers
// (the backend) control failover preference by ordering the slice.
//
// When ResultCode == 3006 but
// RedirectHosts is empty, this is a producer bug — caller decides how
// strict to be; today we tolerate silently (RFC §6.5 leaves "what does
// 3006 without Redirect-Host mean" implementation-defined).
func applyRedirectAVPs(ans *diam.Message, res *pb.S6aAnswer) {
	if len(res.RedirectHosts) == 0 {
		return
	}
	for _, uri := range res.RedirectHosts {
		ans.NewAVP(avp.RedirectHost, avp.Mbit, 0, datatype.DiameterURI(uri))
	}
	if res.RedirectHostUsage != 0 {
		ans.NewAVP(avp.RedirectHostUsage, avp.Mbit, 0, datatype.Enumerated(res.RedirectHostUsage))
	}
	if res.RedirectMaxCacheTime != 0 {
		ans.NewAVP(avp.RedirectMaxCacheTime, avp.Mbit, 0, datatype.Unsigned32(res.RedirectMaxCacheTime))
	}
	monitor.IncrementCounter("diameter_redirect_emitted_total", map[string]string{
		"usage": redirectUsageLabel(res.RedirectHostUsage),
	})
}

// redirectUsageLabel returns the §6.13 enum label so the metric stays
// aligned with RFC vocabulary rather than raw integers.
func redirectUsageLabel(u uint32) string {
	return RedirectHostUsage(u).String()
}

// applyResetIDs emits Reset-ID AVPs (1670, TS 29.272 §7.3.184) on a ULA, the
// only answer whose ABNF carries them (§7.2.4). Each entry is hex-decoded
// into the AVP's OctetString payload. Empty list ⇒ no AVPs emitted. Bad hex on
// a single entry skips that entry but keeps the rest (mirrors
// applyFailedAVPs). go-diameter v4 has no constant for 1670.
func applyResetIDs(ans *diam.Message, ids []string) {
	for _, id := range ids {
		raw, err := hex.DecodeString(id)
		if err != nil {
			continue
		}
		ans.AddAVP(avp3GPP(1670, datatype.OctetString(raw)))
	}
}

// applySupportedFeatures emits one Supported-Features Grouped AVP (628) per
// producer-supplied entry. Each Grouped AVP carries Vendor-Id (266, base) +
// Feature-List-ID (629, vendor 10415) + Feature-List (630, vendor 10415) per
// TS 29.229 §6.2 + TS 29.272 §7.3.10. No-op when entries is empty.
//
// Used on ULA/AIA/PUA/NOA answers (Rel-12+ feature negotiation). Producer
// (the backend) handles the actual intersection of HSS-supported features and the
// incoming MME's advertised features; gateway transports verbatim.
func applySupportedFeatures(ans *diam.Message, sfs []pb.SupportedFeature) {
	for _, sf := range sfs {
		ans.AddAVP(avp3GPP(avp.SupportedFeatures, &diam.GroupedAVP{
			AVP: []*diam.AVP{
				diam.NewAVP(avp.VendorID, avp.Mbit, 0, datatype.Unsigned32(sf.VendorID)),
				avp3GPP(avp.FeatureListID, datatype.Unsigned32(sf.FeatureListID)),
				avp3GPP(avp.FeatureList, datatype.Unsigned32(sf.FeatureList)),
			},
		}))
	}
}

// applyFailedAVPs emits a Failed-AVP grouped AVP (RFC 6733 §7.5, code 279)
// wrapping each producer-supplied entry. No-op when entries is empty.
//
// Per spec, Failed-AVP SHOULD accompany answers carrying AVP-class
// Result-Codes (5004 INVALID_AVP_VALUE, 5005 MISSING_AVP, 5007
// CONTRADICTING_AVPS, 5008 AVP_NOT_ALLOWED, 5009 AVP_OCCURS_TOO_MANY_TIMES,
// 5014 INVALID_AVP_LENGTH). The gateway does not gate emission on
// result-code class — producer (the HSS backend) supplies entries when it wants
// debug-friendly answers; the gateway honors verbatim. This matches the
// "trust the producer" pattern already used for Redirect-Host.
//
// Each FailedAVPEntry's Value is hex-decoded into the wire payload. An
// empty Value emits a zero-length OctetString — peers still see the
// failed AVP code (the part they actually need), without the gateway
// inventing a payload.
func applyFailedAVPs(ans *diam.Message, entries []pb.FailedAVPEntry) {
	if len(entries) == 0 {
		return
	}
	children := make([]*diam.AVP, 0, len(entries))
	for _, e := range entries {
		raw, err := hex.DecodeString(e.Value) // empty Value → empty bytes, no error
		if err != nil {
			// Bad hex from producer is a backend bug — skip THIS entry but
			// keep the rest, so a partial Failed-AVP is still useful for
			// debugging. We don't fail the whole answer because the answer's
			// primary information (the Result-Code) is more important than
			// the debugging hint.
			continue
		}
		flags := uint8(avp.Mbit)
		if e.VendorID != 0 {
			flags |= avp.Vbit
		}
		children = append(children, diam.NewAVP(e.Code, flags, e.VendorID, datatype.OctetString(raw)))
	}
	if len(children) == 0 {
		return
	}
	ans.NewAVP(avp.FailedAVP, avp.Mbit, 0, &diam.GroupedAVP{AVP: children})
}

// applyResultCode chooses between base Result-Code and 3GPP Experimental-
// Result per TS 29.272 §6.2.5 / RFC 6733 §7.6. When ExperimentalResultCode
// is set, the answer header carries Result-Code 0 (no base) and an
// Experimental-Result grouped AVP carries the vendor 10415 code; otherwise
// the base Result-Code is used directly.
func applyResultCode(req *diam.Message, baseRC, expRC uint32) *diam.Message {
	if expRC == 0 {
		return req.Answer(baseRC)
	}
	ans := req.Answer(0) // 0 — answer header carries no base Result-Code
	ans.NewAVP(avp.ExperimentalResult, avp.Mbit, 0, &diam.GroupedAVP{
		AVP: []*diam.AVP{
			diam.NewAVP(avp.VendorID, avp.Mbit, 0, datatype.Unsigned32(10415)),
			diam.NewAVP(avp.ExperimentalResultCode, avp.Mbit, 0, datatype.Unsigned32(expRC)),
		},
	})
	return ans
}

// encodeAuthVectors converts the JSON envelope's hex-encoded EUTRAN
// vectors into a grouped Authentication-Info AVP payload. Returns nil
// when no vectors are supplied (legal — backend may reply with just an
// error Result-Code). Returns an error on any hex decode failure: the
// caller maps this to 5012 (UNABLE_TO_COMPLY) so a corrupt envelope
// fails closed rather than emitting AVPs with zero-padded crypto.
func encodeAuthVectors(vectors []pb.EUTRANVector) (*diam.GroupedAVP, error) {
	if len(vectors) == 0 {
		return nil, nil
	}

	children := make([]*diam.AVP, 0, len(vectors))
	for i, v := range vectors {
		rand, err := hex.DecodeString(v.RAND)
		if err != nil {
			return nil, fmt.Errorf("auth_vectors[%d].rand: %w", i, err)
		}
		xres, err := hex.DecodeString(v.XRES)
		if err != nil {
			return nil, fmt.Errorf("auth_vectors[%d].xres: %w", i, err)
		}
		autn, err := hex.DecodeString(v.AUTN)
		if err != nil {
			return nil, fmt.Errorf("auth_vectors[%d].autn: %w", i, err)
		}
		kasme, err := hex.DecodeString(v.KASME)
		if err != nil {
			return nil, fmt.Errorf("auth_vectors[%d].kasme: %w", i, err)
		}

		children = append(children, avp3GPP(avp.EUTRANVector, &diam.GroupedAVP{
			AVP: []*diam.AVP{
				avp3GPP(avp.ItemNumber, datatype.Unsigned32(v.Item)),
				avp3GPP(avp.RAND, datatype.OctetString(rand)),
				avp3GPP(avp.XRES, datatype.OctetString(xres)),
				avp3GPP(avp.AUTN, datatype.OctetString(autn)),
				avp3GPP(avp.KASME, datatype.OctetString(kasme)),
			},
		}))
	}
	return &diam.GroupedAVP{AVP: children}, nil
}
