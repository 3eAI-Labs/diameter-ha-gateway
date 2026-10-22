// Copyright 2026 3eAI Labs Ltd
// SPDX-License-Identifier: AGPL-3.0-only

package charging

import (
	"time"

	"github.com/fiorix/go-diameter/v4/diam"
	"github.com/fiorix/go-diameter/v4/diam/avp"
	"github.com/fiorix/go-diameter/v4/diam/datatype"
)

// The Credit-Control AVPs Gy and Ro share: Multiple-Services-Credit-Control
// and its service units (TS 32.299 §7.1.9, §7.1.14; RFC 8506 §8.16-8.19),
// Final-Unit-Indication, and the CCA's common AVPs. One codec for both
// interfaces: Ro used to send a subset of what Gy sent.
//
// Flags: the RFC 8506 AVPs carry M (RFC 8506 Table, §8); the TS 32.299 AVPs
// written here are V,M in Table 7.2.0.1 (lines 5560-6176 of V17.1.0:
// 865-874, 881, 1226, 1264-1271, 2020-2022); 3GPP-Charging-Id is V only,
// must not M (TS 29.061 Table 9a).

const vendor3GPP = 10415

// RFC 8506 / RFC 6733 AVP codes (vendor 0).
const (
	codeFilterID              = 11
	codeResultCode            = 268
	codeCCInputOctets         = 412
	codeCCMoney               = 413
	codeCCOutputOctets        = 414
	codeCCServiceSpecificUnit = 417
	codeCCSessionFailover     = 418
	codeCCTime                = 420
	codeCCTotalOctets         = 421
	codeCostInformation       = 423
	codeCostUnit              = 424
	codeCurrencyCode          = 425
	codeExponent              = 429
	codeFinalUnitIndication   = 430
	codeGrantedServiceUnit    = 431
	codeRatingGroup           = 432
	codeRedirectAddressType   = 433
	codeRedirectServer        = 434
	codeRedirectServerAddress = 435
	codeRequestedServiceUnit  = 437
	codeRestrictionFilterRule = 438
	codeServiceIdentifier     = 439
	codeUnitValue             = 445
	codeUsedServiceUnit       = 446
	codeValueDigits           = 447
	codeValidityTime          = 448
	codeFinalUnitAction       = 449
	codeTariffTimeChange      = 451
	codeTariffChangeUsage     = 452
	codeGSUPoolIdentifier     = 453
	codeCCUnitType            = 454
	codeMSCC                  = 456
	codeGSUPoolReference      = 457
	codeFailedAVP             = 279
)

// TS 32.299 AVP codes (vendor 3GPP).
const (
	codePSFurnishChargingInfo = 865
	codePSFreeFormatData      = 866
	codePSAppendFreeFormat    = 867
	codeTimeQuotaThreshold    = 868
	codeVolumeQuotaThreshold  = 869
	codeTriggerType           = 870
	codeQuotaHoldingTime      = 871
	codeReportingReason       = 872
	codeQuotaConsumptionTime  = 881
	codeUnitQuotaThreshold    = 1226
	codeTrigger               = 1264
	codeBaseTimeInterval      = 1265
	codeEnvelope              = 1266
	codeEnvelopeEndTime       = 1267
	codeEnvelopeReporting     = 1268
	codeEnvelopeStartTime     = 1269
	codeTimeQuotaMechanism    = 1270
	codeTimeQuotaType         = 1271
	codeLowBalanceIndication  = 2020
	codeRemainingBalance      = 2021
	codeRefundInformation     = 2022
	code3GPPChargingID        = 2 // TS 29.061
)

// ─── Reading ─────────────────────────────────────────────────────────────

// kids returns a's children; an empty group is non-nil, so a reader tells
// it from an absent AVP (an empty Requested-Service-Unit is common).
func kids(a *diam.AVP) []*diam.AVP {
	if a == nil {
		return nil
	}
	if g, ok := a.Data.(*diam.GroupedAVP); ok {
		if g.AVP == nil {
			return []*diam.AVP{}
		}
		return g.AVP
	}
	return nil
}

func pick(list []*diam.AVP, code, vendor uint32) *diam.AVP {
	for _, a := range list {
		if a.Code == code && a.VendorID == vendor {
			return a
		}
	}
	return nil
}

func pickAll(list []*diam.AVP, code, vendor uint32) []*diam.AVP {
	var out []*diam.AVP
	for _, a := range list {
		if a.Code == code && a.VendorID == vendor {
			out = append(out, a)
		}
	}
	return out
}

func u32ok(a *diam.AVP) (uint32, bool) {
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

func u32(a *diam.AVP) uint32 { v, _ := u32ok(a); return v }

func u32p(a *diam.AVP) *uint32 {
	if v, ok := u32ok(a); ok {
		return &v
	}
	return nil
}

func u64(a *diam.AVP) uint64 {
	if a == nil {
		return 0
	}
	switch v := a.Data.(type) {
	case datatype.Unsigned64:
		return uint64(v)
	case datatype.Unsigned32:
		return uint64(v)
	}
	return 0
}

func i64(a *diam.AVP) int64 {
	if a == nil {
		return 0
	}
	switch v := a.Data.(type) {
	case datatype.Integer64:
		return int64(v)
	case datatype.Integer32:
		return int64(v)
	}
	return 0
}

func unix(a *diam.AVP) int64 {
	if a == nil {
		return 0
	}
	if t, ok := a.Data.(datatype.Time); ok {
		return time.Time(t).Unix()
	}
	return 0
}

func octets(a *diam.AVP) []byte {
	if a == nil {
		return nil
	}
	if v, ok := a.Data.(datatype.OctetString); ok {
		return []byte(v)
	}
	return nil
}

func text(a *diam.AVP) string {
	if a == nil {
		return ""
	}
	switch v := a.Data.(type) {
	case datatype.UTF8String:
		return string(v)
	case datatype.OctetString:
		return string(v)
	case datatype.IPFilterRule:
		return string(v)
	case datatype.DiameterIdentity:
		return string(v)
	}
	return ""
}

func readUnitValue(a *diam.AVP) *UnitValue {
	c := kids(a)
	if c == nil {
		return nil
	}
	return &UnitValue{ValueDigits: i64(pick(c, codeValueDigits, 0)), Exponent: int32(i64(pick(c, codeExponent, 0)))}
}

func readCCMoney(a *diam.AVP) *CCMoney {
	c := kids(a)
	if c == nil {
		return nil
	}
	return &CCMoney{UnitValue: readUnitValue(pick(c, codeUnitValue, 0)), CurrencyCode: u32(pick(c, codeCurrencyCode, 0))}
}

// ReadServiceUnit reads a Granted-, Requested- or Used-Service-Unit.
func ReadServiceUnit(a *diam.AVP) *ServiceUnit {
	c := kids(a)
	if c == nil {
		return nil
	}
	return &ServiceUnit{
		CCTime:                 u32(pick(c, codeCCTime, 0)),
		CCMoney:                readCCMoney(pick(c, codeCCMoney, 0)),
		CCTotalOctets:          u64(pick(c, codeCCTotalOctets, 0)),
		CCInputOctets:          u64(pick(c, codeCCInputOctets, 0)),
		CCOutputOctets:         u64(pick(c, codeCCOutputOctets, 0)),
		CCServiceSpecificUnits: u64(pick(c, codeCCServiceSpecificUnit, 0)),
		TariffTimeChange:       unix(pick(c, codeTariffTimeChange, 0)),
		TariffChangeUsage:      u32p(pick(c, codeTariffChangeUsage, 0)),
		ReportingReason:        u32p(pick(c, codeReportingReason, vendor3GPP)),
	}
}

// ReadMSCCs reads every Multiple-Services-Credit-Control of list (TS
// 32.299 §7.1.9).
func ReadMSCCs(list []*diam.AVP) []MSCC {
	var out []MSCC
	for _, a := range pickAll(list, codeMSCC, 0) {
		c := kids(a)
		m := MSCC{
			GrantedServiceUnit:   ReadServiceUnit(pick(c, codeGrantedServiceUnit, 0)),
			RequestedServiceUnit: ReadServiceUnit(pick(c, codeRequestedServiceUnit, 0)),
			TariffChangeUsage:    u32p(pick(c, codeTariffChangeUsage, 0)),
			RatingGroup:          u32p(pick(c, codeRatingGroup, 0)),
			ValidityTime:         u32(pick(c, codeValidityTime, 0)),
			ResultCode:           u32(pick(c, codeResultCode, 0)),
			FinalUnitIndication:  ReadFinalUnitIndication(pick(c, codeFinalUnitIndication, 0)),
			TimeQuotaThreshold:   u32(pick(c, codeTimeQuotaThreshold, vendor3GPP)),
			VolumeQuotaThreshold: u32(pick(c, codeVolumeQuotaThreshold, vendor3GPP)),
			UnitQuotaThreshold:   u32(pick(c, codeUnitQuotaThreshold, vendor3GPP)),
			QuotaHoldingTime:     u32p(pick(c, codeQuotaHoldingTime, vendor3GPP)),
			QuotaConsumptionTime: u32(pick(c, codeQuotaConsumptionTime, vendor3GPP)),
			RefundInformation:    octets(pick(c, codeRefundInformation, vendor3GPP)),
			EnvelopeReporting:    u32p(pick(c, codeEnvelopeReporting, vendor3GPP)),
		}
		for _, su := range pickAll(c, codeUsedServiceUnit, 0) {
			if u := ReadServiceUnit(su); u != nil {
				m.UsedServiceUnit = append(m.UsedServiceUnit, *u)
			}
		}
		for _, si := range pickAll(c, codeServiceIdentifier, 0) {
			m.ServiceIdentifiers = append(m.ServiceIdentifiers, u32(si))
		}
		if len(m.ServiceIdentifiers) > 0 {
			m.ServiceIdentifier = m.ServiceIdentifiers[0]
		}
		for _, rr := range pickAll(c, codeReportingReason, vendor3GPP) {
			m.ReportingReason = append(m.ReportingReason, u32(rr))
		}
		for _, p := range pickAll(c, codeGSUPoolReference, 0) {
			pc := kids(p)
			m.GSUPoolReference = append(m.GSUPoolReference, GSUPoolRef{
				GSUPoolIdentifier: u32(pick(pc, codeGSUPoolIdentifier, 0)),
				CCUnitType:        u32(pick(pc, codeCCUnitType, 0)),
				UnitValue:         readUnitValue(pick(pc, codeUnitValue, 0)),
			})
		}
		if tr := pick(c, codeTrigger, vendor3GPP); tr != nil {
			t := &TriggerType{}
			for _, tt := range pickAll(kids(tr), codeTriggerType, vendor3GPP) {
				t.TriggerType = append(t.TriggerType, u32(tt))
			}
			m.TriggerType = t
		}
		for _, e := range pickAll(c, codeEnvelope, vendor3GPP) {
			ec := kids(e)
			m.Envelope = append(m.Envelope, Envelope{
				EnvelopeStartTime:      unix(pick(ec, codeEnvelopeStartTime, vendor3GPP)),
				EnvelopeEndTime:        unix(pick(ec, codeEnvelopeEndTime, vendor3GPP)),
				CCTotalOctets:          u64(pick(ec, codeCCTotalOctets, 0)),
				CCInputOctets:          u64(pick(ec, codeCCInputOctets, 0)),
				CCOutputOctets:         u64(pick(ec, codeCCOutputOctets, 0)),
				CCServiceSpecificUnits: u64(pick(ec, codeCCServiceSpecificUnit, 0)),
			})
		}
		if tq := kids(pick(c, codeTimeQuotaMechanism, vendor3GPP)); tq != nil {
			m.TimeQuotaMechanism = &TimeQuotaMechanism{
				TimeQuotaType:    u32(pick(tq, codeTimeQuotaType, vendor3GPP)),
				BaseTimeInterval: u32(pick(tq, codeBaseTimeInterval, vendor3GPP)),
			}
		}
		if pf := kids(pick(c, codePSFurnishChargingInfo, vendor3GPP)); pf != nil {
			m.PSFurnishChargingInfo = &PSFurnishChargingInfo{
				ThreeGPPChargingID:     octets(pick(pf, code3GPPChargingID, vendor3GPP)),
				PSFreeFormatData:       octets(pick(pf, codePSFreeFormatData, vendor3GPP)),
				PSAppendFreeFormatData: u32p(pick(pf, codePSAppendFreeFormat, vendor3GPP)),
			}
		}
		out = append(out, m)
	}
	return out
}

// ReadFinalUnitIndication reads a Final-Unit-Indication (RFC 8506 §8.34).
func ReadFinalUnitIndication(a *diam.AVP) *FinalUnitIndication {
	c := kids(a)
	if c == nil {
		return nil
	}
	f := &FinalUnitIndication{FinalUnitAction: u32(pick(c, codeFinalUnitAction, 0))}
	for _, r := range pickAll(c, codeRestrictionFilterRule, 0) {
		f.RestrictionFilterRule = append(f.RestrictionFilterRule, []byte(text(r)))
	}
	for _, id := range pickAll(c, codeFilterID, 0) {
		f.FilterID = append(f.FilterID, text(id))
	}
	if rs := kids(pick(c, codeRedirectServer, 0)); rs != nil {
		f.RedirectServer = &RedirectServer{
			RedirectAddressType:   u32(pick(rs, codeRedirectAddressType, 0)),
			RedirectServerAddress: text(pick(rs, codeRedirectServerAddress, 0)),
		}
	}
	return f
}

// ─── Writing ─────────────────────────────────────────────────────────────

// cc is an RFC 8506 AVP: vendor 0, M bit.
func cc(code uint32, v datatype.Type) *diam.AVP { return diam.NewAVP(code, avp.Mbit, 0, v) }

// tgpp is a TS 32.299 AVP: vendor 3GPP, V and M.
func tgpp(code uint32, v datatype.Type) *diam.AVP {
	return diam.NewAVP(code, avp.Mbit|avp.Vbit, vendor3GPP, v)
}

func group(list ...*diam.AVP) *diam.GroupedAVP {
	g := &diam.GroupedAVP{}
	for _, a := range list {
		if a != nil {
			g.AddAVP(a)
		}
	}
	return g
}

func nz32(code uint32, v uint32, mk func(uint32, datatype.Type) *diam.AVP) *diam.AVP {
	if v == 0 {
		return nil
	}
	return mk(code, datatype.Unsigned32(v))
}

func nz64(code uint32, v uint64) *diam.AVP {
	if v == 0 {
		return nil
	}
	return cc(code, datatype.Unsigned64(v))
}

func ptrEnum(code uint32, v *uint32, mk func(uint32, datatype.Type) *diam.AVP) *diam.AVP {
	if v == nil {
		return nil
	}
	return mk(code, datatype.Enumerated(*v))
}

func ptrU32(code uint32, v *uint32, mk func(uint32, datatype.Type) *diam.AVP) *diam.AVP {
	if v == nil {
		return nil
	}
	return mk(code, datatype.Unsigned32(*v))
}

// unitValue writes a Unit-Value; the groups that contain one need it
// ({ Unit-Value }), so they are left out without it.
func unitValue(v *UnitValue) *diam.AVP {
	if v == nil {
		return nil
	}
	var exp *diam.AVP
	if v.Exponent != 0 {
		exp = cc(codeExponent, datatype.Integer32(v.Exponent))
	}
	return cc(codeUnitValue, group(cc(codeValueDigits, datatype.Integer64(v.ValueDigits)), exp))
}

func ccMoney(m *CCMoney) *diam.AVP {
	if m == nil || m.UnitValue == nil {
		return nil
	}
	return cc(codeCCMoney, group(unitValue(m.UnitValue), nz32(codeCurrencyCode, m.CurrencyCode, cc)))
}

// WriteServiceUnit writes a Granted-, Requested- or Used-Service-Unit
// (code) with every unit set; Tariff-Time-Change belongs to the GSU,
// Tariff-Change-Usage and Reporting-Reason to the USU.
func WriteServiceUnit(code uint32, su *ServiceUnit) *diam.AVP {
	if su == nil {
		return nil
	}
	var ttc, tcu, rr *diam.AVP
	switch code {
	case codeGrantedServiceUnit:
		if su.TariffTimeChange != 0 {
			ttc = cc(codeTariffTimeChange, datatype.Time(time.Unix(su.TariffTimeChange, 0).UTC()))
		}
	case codeUsedServiceUnit:
		tcu = ptrEnum(codeTariffChangeUsage, su.TariffChangeUsage, cc)
		rr = ptrEnum(codeReportingReason, su.ReportingReason, tgpp)
	}
	return cc(code, group(rr, ttc, tcu,
		nz32(codeCCTime, su.CCTime, cc),
		ccMoney(su.CCMoney),
		nz64(codeCCTotalOctets, su.CCTotalOctets),
		nz64(codeCCInputOctets, su.CCInputOctets),
		nz64(codeCCOutputOctets, su.CCOutputOctets),
		nz64(codeCCServiceSpecificUnit, su.CCServiceSpecificUnits)))
}

// WriteFinalUnitIndication writes a Final-Unit-Indication (RFC 8506 §8.34).
func WriteFinalUnitIndication(f *FinalUnitIndication) *diam.AVP {
	if f == nil {
		return nil
	}
	g := group(cc(codeFinalUnitAction, datatype.Enumerated(f.FinalUnitAction)))
	for _, r := range f.RestrictionFilterRule {
		g.AddAVP(cc(codeRestrictionFilterRule, datatype.IPFilterRule(r)))
	}
	for _, id := range f.FilterID {
		g.AddAVP(cc(codeFilterID, datatype.UTF8String(id)))
	}
	if rs := f.RedirectServer; rs != nil {
		g.AddAVP(cc(codeRedirectServer, group(
			cc(codeRedirectAddressType, datatype.Enumerated(rs.RedirectAddressType)),
			cc(codeRedirectServerAddress, datatype.UTF8String(rs.RedirectServerAddress)))))
	}
	return cc(codeFinalUnitIndication, g)
}

// WriteMSCC writes a Multiple-Services-Credit-Control (TS 32.299 §7.1.9)
// with every field m sets.
func WriteMSCC(m *MSCC) *diam.AVP {
	g := group(
		WriteServiceUnit(codeGrantedServiceUnit, m.GrantedServiceUnit),
		WriteServiceUnit(codeRequestedServiceUnit, m.RequestedServiceUnit))
	for i := range m.UsedServiceUnit {
		g.AddAVP(WriteServiceUnit(codeUsedServiceUnit, &m.UsedServiceUnit[i]))
	}
	if a := ptrEnum(codeTariffChangeUsage, m.TariffChangeUsage, cc); a != nil {
		g.AddAVP(a)
	}
	ids := m.ServiceIdentifiers
	if len(ids) == 0 && m.ServiceIdentifier != 0 {
		ids = []uint32{m.ServiceIdentifier}
	}
	for _, id := range ids {
		g.AddAVP(cc(codeServiceIdentifier, datatype.Unsigned32(id)))
	}
	for _, a := range []*diam.AVP{ptrU32(codeRatingGroup, m.RatingGroup, cc)} {
		if a != nil {
			g.AddAVP(a)
		}
	}
	for _, p := range m.GSUPoolReference {
		g.AddAVP(cc(codeGSUPoolReference, group(
			cc(codeGSUPoolIdentifier, datatype.Unsigned32(p.GSUPoolIdentifier)),
			cc(codeCCUnitType, datatype.Enumerated(p.CCUnitType)),
			unitValue(p.UnitValue))))
	}
	for _, a := range []*diam.AVP{
		nz32(codeValidityTime, m.ValidityTime, cc),
		nz32(codeResultCode, m.ResultCode, cc),
		WriteFinalUnitIndication(m.FinalUnitIndication),
		nz32(codeTimeQuotaThreshold, m.TimeQuotaThreshold, tgpp),
		nz32(codeVolumeQuotaThreshold, m.VolumeQuotaThreshold, tgpp),
		nz32(codeUnitQuotaThreshold, m.UnitQuotaThreshold, tgpp),
		ptrU32(codeQuotaHoldingTime, m.QuotaHoldingTime, tgpp),
		nz32(codeQuotaConsumptionTime, m.QuotaConsumptionTime, tgpp),
	} {
		if a != nil {
			g.AddAVP(a)
		}
	}
	for _, rr := range m.ReportingReason {
		g.AddAVP(tgpp(codeReportingReason, datatype.Enumerated(rr)))
	}
	if m.TriggerType != nil {
		// An empty Trigger disarms every trigger armed before (§7.2.235).
		t := group()
		for _, tt := range m.TriggerType.TriggerType {
			t.AddAVP(tgpp(codeTriggerType, datatype.Enumerated(tt)))
		}
		g.AddAVP(tgpp(codeTrigger, t))
	}
	// { 3GPP-Charging-Id } is mandatory (TS 32.299 §7.2.157): without it
	// the group is left out rather than sent malformed.
	if pf := m.PSFurnishChargingInfo; pf != nil && len(pf.ThreeGPPChargingID) > 0 {
		g.AddAVP(tgpp(codePSFurnishChargingInfo, group(
			// A RADIUS VSA of TS 29.061 takes the M bit in charging (TS
			// 32.299 §7.2.0).
			tgpp(code3GPPChargingID, datatype.OctetString(pf.ThreeGPPChargingID)),
			tgpp(codePSFreeFormatData, datatype.OctetString(pf.PSFreeFormatData)),
			ptrEnum(codePSAppendFreeFormat, pf.PSAppendFreeFormatData, tgpp))))
	}
	if len(m.RefundInformation) > 0 {
		g.AddAVP(tgpp(codeRefundInformation, datatype.OctetString(m.RefundInformation)))
	}
	for _, e := range m.Envelope {
		var end *diam.AVP
		if e.EnvelopeEndTime != 0 {
			end = tgpp(codeEnvelopeEndTime, datatype.Time(time.Unix(e.EnvelopeEndTime, 0).UTC()))
		}
		g.AddAVP(tgpp(codeEnvelope, group(
			tgpp(codeEnvelopeStartTime, datatype.Time(time.Unix(e.EnvelopeStartTime, 0).UTC())), end,
			nz64(codeCCTotalOctets, e.CCTotalOctets), nz64(codeCCInputOctets, e.CCInputOctets),
			nz64(codeCCOutputOctets, e.CCOutputOctets), nz64(codeCCServiceSpecificUnit, e.CCServiceSpecificUnits))))
	}
	if a := ptrEnum(codeEnvelopeReporting, m.EnvelopeReporting, tgpp); a != nil {
		g.AddAVP(a)
	}
	if tq := m.TimeQuotaMechanism; tq != nil {
		g.AddAVP(tgpp(codeTimeQuotaMechanism, group(
			tgpp(codeTimeQuotaType, datatype.Enumerated(tq.TimeQuotaType)),
			tgpp(codeBaseTimeInterval, datatype.Unsigned32(tq.BaseTimeInterval)))))
	}
	return cc(codeMSCC, g)
}

// CCATail is what a Gy and a Ro CCA share beyond the MSCCs.
type CCATail struct {
	CCSessionFailover    *uint32
	LowBalanceIndication *uint32
	RemainingBalance     *CCMoney
	CostInformation      *CostInformation
	FailedAVP            []FailedAVP
}

// ApplyCCATail writes t to a CCA: CC-Session-Failover and Cost-Information
// (RFC 8506), Low-Balance-Indication and Remaining-Balance (TS 32.299
// §7.2.97, §7.2.172), and one Failed-AVP holding every failed AVP (RFC 6733
// §7.5: "This field can occur only once", TS 32.299 CCA).
func ApplyCCATail(ans *diam.Message, t CCATail) {
	if a := ptrEnum(codeCCSessionFailover, t.CCSessionFailover, cc); a != nil {
		ans.AddAVP(a)
	}
	if c := t.CostInformation; c != nil && c.UnitValue != nil {
		var unit *diam.AVP
		if c.CostUnit != "" {
			unit = cc(codeCostUnit, datatype.UTF8String(c.CostUnit))
		}
		ans.AddAVP(cc(codeCostInformation, group(unitValue(c.UnitValue),
			cc(codeCurrencyCode, datatype.Unsigned32(c.CurrencyCode)), unit)))
	}
	if a := ptrEnum(codeLowBalanceIndication, t.LowBalanceIndication, tgpp); a != nil {
		ans.AddAVP(a)
	}
	if rb := t.RemainingBalance; rb != nil && rb.UnitValue != nil {
		ans.AddAVP(tgpp(codeRemainingBalance, group(unitValue(rb.UnitValue),
			cc(codeCurrencyCode, datatype.Unsigned32(rb.CurrencyCode)))))
	}
	if len(t.FailedAVP) > 0 {
		g := group()
		for _, f := range t.FailedAVP {
			flags := uint8(avp.Mbit)
			if f.VendorID != 0 {
				flags |= avp.Vbit
			}
			g.AddAVP(diam.NewAVP(f.Code, flags, f.VendorID, datatype.OctetString(f.Data)))
		}
		ans.AddAVP(diam.NewAVP(codeFailedAVP, avp.Mbit, 0, g))
	}
}

// AnswerFor opens a CCA: Result-Code, which TS 32.299 §6.4.3 makes
// mandatory, and an Experimental-Result of 3GPP when experimentalCode is
// set (§7.1.8A: 5011 DIAMETER_ERROR_FEATURE_UNSUPPORTED, §6.5.10). With
// resultCode 0 only the Experimental-Result is sent (RFC 6733 §7.6).
func AnswerFor(req *diam.Message, resultCode, experimentalCode uint32) *diam.Message {
	ans := req.Answer(resultCode) // Answer(0) adds no Result-Code
	if experimentalCode != 0 {
		ans.AddAVP(diam.NewAVP(avp.ExperimentalResult, avp.Mbit, 0, group(
			diam.NewAVP(avp.VendorID, avp.Mbit, 0, datatype.Unsigned32(vendor3GPP)),
			diam.NewAVP(avp.ExperimentalResultCode, avp.Mbit, 0, datatype.Unsigned32(experimentalCode)))))
	}
	return ans
}

// U32 and U64 read an integer AVP whatever its integer type; nil or
// another type reads as 0. Gy and Ro use them so a CCR's AVP of an
// unexpected type cannot panic the handler.
func U32(a *diam.AVP) uint32 { return u32(a) }

func U64(a *diam.AVP) uint64 { return u64(a) }
