// Copyright 2026 3eAI Labs Ltd
// SPDX-License-Identifier: AGPL-3.0-only

package sgd

import (
	"strings"
	"time"

	"github.com/3eAI-Labs/diameter-ha-gateway/internal/charging"
	"github.com/3eAI-Labs/diameter-ha-gateway/internal/diameter"
	"github.com/fiorix/go-diameter/v4/diam"
	"github.com/fiorix/go-diameter/v4/diam/avp"
	"github.com/fiorix/go-diameter/v4/diam/datatype"
)

// ─── Reading ─────────────────────────────────────────────────────────────
//
// The readers accept whatever type an AVP decoded as and never panic: a
// peer's AVP of an unexpected type reads as absent.

type avpList []*diam.AVP

// get returns the first AVP with code; vendor 3GPP unless base is set.
func (l avpList) get(code uint32) *diam.AVP { return l.find(code, VendorID3GPP) }

func (l avpList) base(code uint32) *diam.AVP { return l.find(code, 0) }

func (l avpList) find(code, vendor uint32) *diam.AVP {
	for _, a := range l {
		if a.Code == code && a.VendorID == vendor {
			return a
		}
	}
	return nil
}

func (l avpList) all(code uint32) []*diam.AVP {
	var out []*diam.AVP
	for _, a := range l {
		if a.Code == code && a.VendorID == VendorID3GPP {
			out = append(out, a)
		}
	}
	return out
}

func children(a *diam.AVP) avpList {
	if a == nil {
		return nil
	}
	if g, ok := a.Data.(*diam.GroupedAVP); ok {
		return g.AVP
	}
	return nil
}

func asString(a *diam.AVP) string {
	if a == nil {
		return ""
	}
	switch v := a.Data.(type) {
	case datatype.UTF8String:
		return string(v)
	case datatype.DiameterIdentity:
		return string(v)
	case datatype.OctetString:
		return string(v)
	case datatype.DiameterURI:
		return string(v)
	}
	return ""
}

func asBytes(a *diam.AVP) []byte {
	if a == nil {
		return nil
	}
	switch v := a.Data.(type) {
	case datatype.OctetString:
		return []byte(v)
	case datatype.Unknown:
		return []byte(v)
	}
	return nil
}

func asUint32(a *diam.AVP) (uint32, bool) {
	if a == nil {
		return 0, false
	}
	switch v := a.Data.(type) {
	case datatype.Unsigned32:
		return uint32(v), true
	case datatype.Enumerated:
		return uint32(v), true
	case datatype.Integer32:
		return uint32(v), true
	}
	return 0, false
}

func u32Of(a *diam.AVP) uint32 { v, _ := asUint32(a); return v }

func u32Ptr(a *diam.AVP) *uint32 {
	if v, ok := asUint32(a); ok {
		return &v
	}
	return nil
}

func unixOf(a *diam.AVP) int64 {
	if a == nil {
		return 0
	}
	if t, ok := a.Data.(datatype.Time); ok {
		return time.Time(t).Unix()
	}
	return 0
}

func tbcdOf(a *diam.AVP) string { return decodeTBCD(asBytes(a)) }

// header fills the envelope header every event carries.
func header(m *diam.Message, traceID string) charging.DiameterHeader {
	l := avpList(m.AVP)
	return charging.DiameterHeader{
		SessionID:         asString(l.base(avp.SessionID)),
		OriginHost:        asString(l.base(avp.OriginHost)),
		OriginRealm:       asString(l.base(avp.OriginRealm)),
		DestinationHost:   asString(l.base(avp.DestinationHost)),
		DestinationRealm:  asString(l.base(avp.DestinationRealm)),
		AuthApplicationID: m.Header.ApplicationID,
		HopByHop:          m.Header.HopByHopID,
		EndToEnd:          m.Header.EndToEndID,
		TraceID:           traceID,
		Timestamp:         time.Now(),
	}
}

func readUserIdentifier(a *diam.AVP) *UserIdentifier {
	c := children(a)
	if c == nil {
		return nil
	}
	return &UserIdentifier{
		UserName:           asString(c.base(avpUserName)),
		MSISDN:             tbcdOf(c.get(avpMSISDN)),
		ExternalIdentifier: asString(c.get(avpExternalIdentifier)),
		LMSI:               asBytes(c.get(avpLMSI)),
	}
}

func readServingNode(a *diam.AVP) *ServingNode {
	c := children(a)
	if c == nil {
		return nil
	}
	return &ServingNode{
		SGSNName:          asString(c.get(avpSGSNName)),
		SGSNRealm:         asString(c.get(avpSGSNRealm)),
		SGSNNumber:        tbcdOf(c.get(avpSGSNNumber)),
		MMEName:           asString(c.get(avpMMEName)),
		MMERealm:          asString(c.get(avpMMERealm)),
		MMENumberForMTSMS: tbcdOf(c.get(avpMMENumberForMTSMS)),
		MSCNumber:         tbcdOf(c.get(avpMSCNumber)),
		IPSMGWNumber:      tbcdOf(c.get(avpIPSMGWNumber)),
		IPSMGWName:        asString(c.get(avpIPSMGWName)),
		IPSMGWRealm:       asString(c.get(avpIPSMGWRealm)),
	}
}

func readSMSFAddress(a *diam.AVP, number, name, realm uint32) *SMSFAddress {
	c := children(a)
	if c == nil {
		return nil
	}
	return &SMSFAddress{
		Number: tbcdOf(c.get(number)),
		Name:   asString(c.get(name)),
		Realm:  asString(c.get(realm)),
	}
}

func readSupportedFeatures(l avpList) []SupportedFeatures {
	var out []SupportedFeatures
	for _, a := range l.all(avpSupportedFeatures) {
		c := children(a)
		out = append(out, SupportedFeatures{
			VendorID:      u32Of(c.base(avpVendorID)),
			FeatureListID: u32Of(c.get(avpFeatureListID)),
			FeatureList:   u32Of(c.get(avpFeatureList)),
		})
	}
	return out
}

func readSMSMICorrelationID(a *diam.AVP) *SMSMICorrelationID {
	c := children(a)
	if c == nil {
		return nil
	}
	return &SMSMICorrelationID{
		HSSID:             asString(c.get(avpHSSID)),
		OriginatingSIPURI: asString(c.get(avpOriginatingSIPURI)),
		DestinationSIPURI: asString(c.get(avpDestinationSIPURI)),
	}
}

func readDeliveryOutcome(a *diam.AVP) *DeliveryOutcome {
	c := children(a)
	if c == nil {
		return nil
	}
	return &DeliveryOutcome{
		Cause:                  u32Ptr(c.get(avpSMDeliveryCause)),
		AbsentUserDiagnosticSM: u32Ptr(c.get(avpAbsentUserDiagnosticSM)),
	}
}

func readSMDeliveryOutcome(a *diam.AVP) *SMDeliveryOutcome {
	c := children(a)
	if c == nil {
		return nil
	}
	return &SMDeliveryOutcome{
		MME:         readDeliveryOutcome(c.get(avpMMESMDeliveryOutcome)),
		MSC:         readDeliveryOutcome(c.get(avpMSCSMDeliveryOutcome)),
		SGSN:        readDeliveryOutcome(c.get(avpSGSNSMDeliveryOutcome)),
		IPSMGW:      readDeliveryOutcome(c.get(avpIPSMGWSMDeliveryOutcome)),
		SMSF3GPP:    readDeliveryOutcome(c.get(avpSMSF3GPPSMDeliveryOutcome)),
		SMSFNon3GPP: readDeliveryOutcome(c.get(avpSMSFNon3GPPSMDelOutcome)),
	}
}

func readSMDeliveryFailureCause(a *diam.AVP) *SMDeliveryFailureCause {
	c := children(a)
	if c == nil {
		return nil
	}
	return &SMDeliveryFailureCause{
		Cause:          u32Of(c.get(avpSMEnumDeliveryFailure)),
		DiagnosticInfo: asBytes(c.get(avpSMDiagnosticInfo)),
	}
}

// readResult reports a peer's answer. ResultCode holds the
// Experimental-Result-Code when there is one.
func readResult(m *diam.Message) Result {
	rc, exp := diameter.AnswerResult(m)
	if exp != 0 {
		return Result{ResultCode: exp, ExperimentalResult: &ExperimentalResult{
			VendorID: VendorID3GPP, ExperimentalResultCode: exp,
		}}
	}
	return Result{ResultCode: rc}
}

// ─── Writing ─────────────────────────────────────────────────────────────

// writer builds the 3GPP AVPs of one application, with the M bit that
// application gives them. Every builder returns nil for an empty value, and
// add skips nil, so optional AVPs are written only when present.
type writer struct{ app uint32 }

func (w writer) flags(code uint32) uint8 {
	d := defByCode[code]
	if (w.app == AppIDS6c && d.mS6c) || (w.app == AppIDSGd && d.mSGd) {
		return avp.Mbit | avp.Vbit
	}
	return avp.Vbit
}

func (w writer) avp(code uint32, v datatype.Type) *diam.AVP {
	return diam.NewAVP(code, w.flags(code), VendorID3GPP, v)
}

// str writes s with the AVP's own string type.
func (w writer) str(code uint32, s string) *diam.AVP {
	if s == "" {
		return nil
	}
	switch defByCode[code].typ {
	case "DiameterIdentity":
		return w.avp(code, datatype.DiameterIdentity(s))
	case "UTF8String":
		return w.avp(code, datatype.UTF8String(s))
	}
	return w.avp(code, datatype.OctetString(s))
}

func (w writer) octets(code uint32, b []byte) *diam.AVP {
	if len(b) == 0 {
		return nil
	}
	return w.avp(code, datatype.OctetString(b))
}

// tbcd writes an E.164 number; one with no digit is omitted.
func (w writer) tbcd(code uint32, digits string) *diam.AVP {
	b := encodeTBCD(digits)
	if len(b) == 0 {
		return nil
	}
	return w.avp(code, datatype.OctetString(b))
}

// u32 writes a bit mask or counter; 0 is omitted.
func (w writer) u32(code uint32, v uint32) *diam.AVP {
	if v == 0 {
		return nil
	}
	return w.avp(code, datatype.Unsigned32(v))
}

// u32p writes an Unsigned32 whose presence matters.
func (w writer) u32p(code uint32, v *uint32) *diam.AVP {
	if v == nil {
		return nil
	}
	return w.avp(code, datatype.Unsigned32(*v))
}

func (w writer) enum(code uint32, v *uint32) *diam.AVP {
	if v == nil {
		return nil
	}
	return w.avp(code, datatype.Enumerated(int32(*v)))
}

func (w writer) unix(code uint32, sec int64) *diam.AVP {
	if sec == 0 {
		return nil
	}
	return w.avp(code, datatype.Time(time.Unix(sec, 0).UTC()))
}

// group returns a Grouped AVP of the non-nil children, or nil if none.
func (w writer) group(code uint32, list ...*diam.AVP) *diam.AVP {
	var kids []*diam.AVP
	for _, a := range list {
		if a != nil {
			kids = append(kids, a)
		}
	}
	if len(kids) == 0 {
		return nil
	}
	return w.avp(code, &diam.GroupedAVP{AVP: kids})
}

func userNameAVP(imsi string) *diam.AVP {
	if imsi == "" {
		return nil
	}
	return diam.NewAVP(avpUserName, avp.Mbit, 0, datatype.UTF8String(imsi))
}

func (w writer) userIdentifier(u *UserIdentifier) *diam.AVP {
	if u == nil {
		return nil
	}
	return w.group(avpUserIdentifier,
		userNameAVP(u.UserName),
		w.tbcd(avpMSISDN, u.MSISDN),
		w.str(avpExternalIdentifier, u.ExternalIdentifier),
		w.octets(avpLMSI, u.LMSI))
}

// servingNode writes a Serving-Node (code avpServingNode) or an
// Additional-Serving-Node, which has no IP-SM-GW fields.
func (w writer) servingNode(code uint32, n *ServingNode) *diam.AVP {
	if n == nil {
		return nil
	}
	list := []*diam.AVP{
		w.str(avpSGSNName, n.SGSNName),
		w.str(avpSGSNRealm, n.SGSNRealm),
		w.tbcd(avpSGSNNumber, n.SGSNNumber),
		w.str(avpMMEName, n.MMEName),
		w.str(avpMMERealm, n.MMERealm),
		w.tbcd(avpMMENumberForMTSMS, n.MMENumberForMTSMS),
		w.tbcd(avpMSCNumber, n.MSCNumber),
	}
	if code == avpServingNode {
		list = append(list,
			w.tbcd(avpIPSMGWNumber, n.IPSMGWNumber),
			w.str(avpIPSMGWName, n.IPSMGWName),
			w.str(avpIPSMGWRealm, n.IPSMGWRealm))
	}
	return w.group(code, list...)
}

func (w writer) smsfAddress(code, number, name, realm uint32, a *SMSFAddress) *diam.AVP {
	if a == nil {
		return nil
	}
	return w.group(code, w.tbcd(number, a.Number), w.str(name, a.Name), w.str(realm, a.Realm))
}

func (w writer) supportedFeatures(list []SupportedFeatures) []*diam.AVP {
	var out []*diam.AVP
	for _, f := range list {
		vendor := f.VendorID
		if vendor == 0 {
			vendor = VendorID3GPP
		}
		out = append(out, w.avp(avpSupportedFeatures, &diam.GroupedAVP{AVP: []*diam.AVP{
			diam.NewAVP(avpVendorID, avp.Mbit, 0, datatype.Unsigned32(vendor)),
			w.avp(avpFeatureListID, datatype.Unsigned32(f.FeatureListID)),
			w.avp(avpFeatureList, datatype.Unsigned32(f.FeatureList)),
		}}))
	}
	return out
}

func (w writer) smsmiCorrelationID(c *SMSMICorrelationID) *diam.AVP {
	if c == nil {
		return nil
	}
	return w.group(avpSMSMICorrelationID,
		w.str(avpHSSID, c.HSSID),
		w.str(avpOriginatingSIPURI, c.OriginatingSIPURI),
		w.str(avpDestinationSIPURI, c.DestinationSIPURI))
}

func (w writer) deliveryOutcome(code uint32, o *DeliveryOutcome) *diam.AVP {
	if o == nil {
		return nil
	}
	return w.group(code, w.enum(avpSMDeliveryCause, o.Cause), w.u32p(avpAbsentUserDiagnosticSM, o.AbsentUserDiagnosticSM))
}

func (w writer) smDeliveryOutcome(o *SMDeliveryOutcome) *diam.AVP {
	if o == nil {
		return nil
	}
	return w.group(avpSMDeliveryOutcome,
		w.deliveryOutcome(avpMMESMDeliveryOutcome, o.MME),
		w.deliveryOutcome(avpMSCSMDeliveryOutcome, o.MSC),
		w.deliveryOutcome(avpSGSNSMDeliveryOutcome, o.SGSN),
		w.deliveryOutcome(avpIPSMGWSMDeliveryOutcome, o.IPSMGW),
		w.deliveryOutcome(avpSMSF3GPPSMDeliveryOutcome, o.SMSF3GPP),
		w.deliveryOutcome(avpSMSFNon3GPPSMDelOutcome, o.SMSFNon3GPP))
}

func (w writer) smDeliveryFailureCause(c *SMDeliveryFailureCause) *diam.AVP {
	if c == nil {
		return nil
	}
	cause := c.Cause
	return w.group(avpSMDeliveryFailureCause,
		w.enum(avpSMEnumDeliveryFailure, &cause),
		w.octets(avpSMDiagnosticInfo, c.DiagnosticInfo))
}

// add appends the non-nil AVPs with AddAVP, which keeps the header's
// Message-Length right; appending to m.AVP directly does not.
func add(m *diam.Message, list ...*diam.AVP) {
	for _, a := range list {
		if a != nil {
			m.AddAVP(a)
		}
	}
}

// answerFor starts the answer to req with the application's result: an
// Experimental-Result, which TS 29.338 §7.3.1 sends without Result-Code, or a
// Result-Code. diameter.CompleteAnswer adds the rest of the common AVPs.
//
// A TS 29.338 code given as result_code, as the gateway reports a peer's
// Experimental-Result to applications, also goes into Experimental-Result:
// 5550-5558 are no base Result-Code. 5001 stays a Result-Code: in RFC 6733
// it is DIAMETER_AVP_UNSUPPORTED.
func answerFor(req *diam.Message, r Result) *diam.Message {
	if r.ExperimentalResult == nil && r.ResultCode >= DiameterErrorAbsentUser && r.ResultCode <= DiameterErrorMWDListFull {
		r.ExperimentalResult = &ExperimentalResult{VendorID: VendorID3GPP, ExperimentalResultCode: r.ResultCode}
	}
	if r.ExperimentalResult != nil && r.ExperimentalResult.ExperimentalResultCode != 0 {
		vendor := r.ExperimentalResult.VendorID
		if vendor == 0 {
			vendor = VendorID3GPP
		}
		ans := req.Answer(0)
		ans.AddAVP(diam.NewAVP(avp.ExperimentalResult, avp.Mbit, 0, &diam.GroupedAVP{AVP: []*diam.AVP{
			diam.NewAVP(avp.VendorID, avp.Mbit, 0, datatype.Unsigned32(vendor)),
			diam.NewAVP(avp.ExperimentalResultCode, avp.Mbit, 0, datatype.Unsigned32(r.ExperimentalResult.ExperimentalResultCode)),
		}}))
		return ans
	}
	return req.Answer(r.ResultCode)
}

// echoDRMP copies the request's DRMP (RFC 7944) into its answer.
func echoDRMP(ans, req *diam.Message) {
	if a := avpList(req.AVP).base(avpDRMP); a != nil {
		ans.AddAVP(diam.NewAVP(avpDRMP, 0, 0, a.Data))
	}
}

// ─── TBCD ────────────────────────────────────────────────────────────────

// encodeTBCD encodes the digits of an E.164 number as the TBCD-STRING of TS
// 29.002: two digits per octet, the first in the low nibble, an odd length
// padded with 0xF. A leading "+" and anything but 0-9 * # a b c are
// dropped.
func encodeTBCD(number string) []byte {
	var nibbles []byte
	for _, r := range strings.TrimPrefix(number, "+") {
		switch {
		case r >= '0' && r <= '9':
			nibbles = append(nibbles, byte(r-'0'))
		case r == '*':
			nibbles = append(nibbles, 0xA)
		case r == '#':
			nibbles = append(nibbles, 0xB)
		case r >= 'a' && r <= 'c':
			nibbles = append(nibbles, byte(r-'a')+0xC)
		}
	}
	if len(nibbles)%2 == 1 {
		nibbles = append(nibbles, 0xF)
	}
	out := make([]byte, len(nibbles)/2)
	for i := range out {
		out[i] = nibbles[2*i] | nibbles[2*i+1]<<4
	}
	return out
}

// decodeTBCD reverses encodeTBCD; it stops at the 0xF filler.
func decodeTBCD(b []byte) string {
	const digits = "0123456789*#abc"
	var s strings.Builder
	for _, o := range b {
		for _, n := range []byte{o & 0x0F, o >> 4} {
			if n == 0xF {
				return s.String()
			}
			s.WriteByte(digits[n])
		}
	}
	return s.String()
}
