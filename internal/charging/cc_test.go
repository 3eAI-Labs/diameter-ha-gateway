// Copyright 2026 3eAI Labs Ltd
// SPDX-License-Identifier: AGPL-3.0-only

package charging

import (
	"bytes"
	"testing"

	"github.com/fiorix/go-diameter/v4/diam"
	"github.com/fiorix/go-diameter/v4/diam/avp"
	"github.com/fiorix/go-diameter/v4/diam/datatype"
	"github.com/fiorix/go-diameter/v4/diam/dict"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func p32(v uint32) *uint32 { return &v }

// onWire puts list in a CCA (application 4), serialises it and reads it
// back; no AVP may decode as Unknown, at any depth.
func onWire(t *testing.T, list ...*diam.AVP) *diam.Message {
	t.Helper()
	m := diam.NewMessage(272, 0, 4, 1, 2, dict.Default)
	for _, a := range list {
		m.AddAVP(a)
	}
	b, err := m.Serialize()
	require.NoError(t, err)
	out, err := diam.ReadMessage(bytes.NewReader(b), dict.Default)
	require.NoError(t, err)
	var walk func([]*diam.AVP)
	walk = func(l []*diam.AVP) {
		for _, a := range l {
			_, unknown := a.Data.(datatype.Unknown)
			assert.False(t, unknown, "AVP %d/%d decoded as Unknown", a.Code, a.VendorID)
			if g, ok := a.Data.(*diam.GroupedAVP); ok {
				walk(g.AVP)
			}
		}
	}
	walk(out.AVP)
	return out
}

// TestMSCC_RoundTrip: every MSCC field of TS 32.299 §7.1.9 the codec
// carries survives the wire, value-0 fields included. Gy wrote a subset of
// them, Ro a smaller one; neither could send Quota-Holding-Time 0 or leave
// Rating-Group out.
func TestMSCC_RoundTrip(t *testing.T) {
	in := MSCC{
		GrantedServiceUnit: &ServiceUnit{CCTime: 60, CCTotalOctets: 1000, TariffTimeChange: 1792300000,
			CCMoney: &CCMoney{UnitValue: &UnitValue{ValueDigits: 150, Exponent: -2}, CurrencyCode: 949}},
		RequestedServiceUnit: &ServiceUnit{CCTime: 30},
		UsedServiceUnit:      []ServiceUnit{{CCTime: 10, ReportingReason: p32(0), TariffChangeUsage: p32(0)}, {CCInputOctets: 5}},
		ServiceIdentifiers:   []uint32{1, 2},
		RatingGroup:          p32(0),
		GSUPoolReference:     []GSUPoolRef{{GSUPoolIdentifier: 7, CCUnitType: 1, UnitValue: &UnitValue{ValueDigits: 3}}},
		ValidityTime:         600, ResultCode: 2001,
		FinalUnitIndication: &FinalUnitIndication{FinalUnitAction: 1,
			RedirectServer: &RedirectServer{RedirectAddressType: 2, RedirectServerAddress: "http://topup"}},
		TimeQuotaThreshold: 5, VolumeQuotaThreshold: 6, UnitQuotaThreshold: 7,
		QuotaHoldingTime: p32(0), QuotaConsumptionTime: 8,
		ReportingReason:       []uint32{2, 3},
		TriggerType:           &TriggerType{TriggerType: []uint32{1, 2}},
		PSFurnishChargingInfo: &PSFurnishChargingInfo{ThreeGPPChargingID: []byte{1}, PSFreeFormatData: []byte{2}, PSAppendFreeFormatData: p32(1)},
		RefundInformation:     []byte{9},
		Envelope:              []Envelope{{EnvelopeStartTime: 1792300000, EnvelopeEndTime: 1792300060, CCTotalOctets: 11}},
		EnvelopeReporting:     p32(0),
		TimeQuotaMechanism:    &TimeQuotaMechanism{TimeQuotaType: 1, BaseTimeInterval: 30},
	}
	got := ReadMSCCs(onWire(t, WriteMSCC(&in)).AVP)
	require.Len(t, got, 1)
	out := got[0]
	in.ServiceIdentifier = 1 // filled from the first Service-Identifier
	assert.Equal(t, in, out)
}

// TestMSCC_AbsentAndEmpty: Rating-Group and Quota-Holding-Time absent are
// not written (QHT absent leaves the client's default; QHT 0 disables the
// mechanism, TS 32.299 §7.2.160); an empty Trigger is written, disarming
// every trigger (§7.2.235).
func TestMSCC_AbsentAndEmpty(t *testing.T) {
	a := WriteMSCC(&MSCC{GrantedServiceUnit: &ServiceUnit{CCTime: 1}, TriggerType: &TriggerType{}})
	var codes []uint32
	for _, c := range a.Data.(*diam.GroupedAVP).AVP {
		codes = append(codes, c.Code)
	}
	assert.NotContains(t, codes, uint32(codeRatingGroup))
	assert.NotContains(t, codes, uint32(codeQuotaHoldingTime))
	assert.Contains(t, codes, uint32(codeTrigger))

	got := ReadMSCCs(onWire(t, a).AVP)
	require.NotNil(t, got[0].TriggerType)
	assert.Empty(t, got[0].TriggerType.TriggerType)
	assert.Nil(t, got[0].RatingGroup)
}

// TestApplyCCATail: one Failed-AVP holding every failed AVP, with their
// flags (RFC 6733 §7.5; TS 32.299: "This field can occur only once"), and
// the CCA's value-0 enums sent.
func TestApplyCCATail(t *testing.T) {
	m := diam.NewMessage(272, 0, 4, 1, 2, dict.Default)
	ApplyCCATail(m, CCATail{
		CCSessionFailover:    p32(0),
		LowBalanceIndication: p32(0),
		RemainingBalance:     &CCMoney{UnitValue: &UnitValue{ValueDigits: 500}, CurrencyCode: 949},
		CostInformation:      &CostInformation{UnitValue: &UnitValue{ValueDigits: 25, Exponent: -1}, CurrencyCode: 949, CostUnit: "minute"},
		FailedAVP:            []FailedAVP{{Code: 416, Data: []byte{0, 0, 0, 1}}, {Code: 872, VendorID: vendor3GPP, Data: []byte{0, 0, 0, 0}}},
	})
	got := onWire(t, m.AVP...)

	failed := pickAll(got.AVP, codeFailedAVP, 0)
	require.Len(t, failed, 1, "one Failed-AVP")
	kidsOf := kids(failed[0])
	require.Len(t, kidsOf, 2)
	assert.Equal(t, uint8(avp.Mbit|avp.Vbit), kidsOf[1].Flags, "a vendor AVP keeps V")
	assert.Equal(t, p32(0), u32p(pick(got.AVP, codeCCSessionFailover, 0)), "FAILOVER_NOT_SUPPORTED (0) is sent")
	assert.Equal(t, p32(0), u32p(pick(got.AVP, codeLowBalanceIndication, vendor3GPP)), "NOT-APPLICABLE (0) is sent")
	assert.Equal(t, "minute", text(pick(kids(pick(got.AVP, codeCostInformation, 0)), codeCostUnit, 0)))
	assert.Equal(t, int64(500), readUnitValue(pick(kids(pick(got.AVP, codeRemainingBalance, vendor3GPP)), codeUnitValue, 0)).ValueDigits)
}

// TestReviewFixes: an empty Requested-Service-Unit reads as present; a
// PS-Furnish-Charging-Information without its mandatory 3GPP-Charging-Id,
// and a Cost-Information without its Unit-Value, are not sent;
// 3GPP-Charging-Id carries V only (TS 29.061); PS-Append-Free-Format-Data
// absent is not sent (absent means 'Overwrite', 0 'Append').
func TestReviewFixes(t *testing.T) {
	m := ReadMSCCs(onWire(t, cc(codeMSCC, group(cc(codeRequestedServiceUnit, group())))).AVP)
	require.Len(t, m, 1)
	assert.NotNil(t, m[0].RequestedServiceUnit, "an empty RSU is present")

	a := WriteMSCC(&MSCC{PSFurnishChargingInfo: &PSFurnishChargingInfo{PSFreeFormatData: []byte{1}}})
	assert.Nil(t, pick(kids(a), codePSFurnishChargingInfo, vendor3GPP), "no group without 3GPP-Charging-Id")

	a = WriteMSCC(&MSCC{PSFurnishChargingInfo: &PSFurnishChargingInfo{ThreeGPPChargingID: []byte{1}, PSFreeFormatData: []byte{2}}})
	pf := kids(pick(kids(a), codePSFurnishChargingInfo, vendor3GPP))
	assert.Equal(t, uint8(avp.Vbit), pick(pf, code3GPPChargingID, vendor3GPP).Flags)
	assert.Nil(t, pick(pf, codePSAppendFreeFormat, vendor3GPP))

	msg := diam.NewMessage(272, 0, 4, 1, 2, dict.Default)
	ApplyCCATail(msg, CCATail{CostInformation: &CostInformation{CurrencyCode: 949}})
	assert.Nil(t, pick(msg.AVP, codeCostInformation, 0))
}
