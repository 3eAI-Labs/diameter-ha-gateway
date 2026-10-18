// Copyright 2026 3eAI Labs Ltd
// SPDX-License-Identifier: AGPL-3.0-only

package gx

import (
	"bytes"
	"strconv"
	"testing"

	"github.com/3eAI-Labs/diameter-ha-gateway/internal/charging"
	"github.com/fiorix/go-diameter/v4/diam"
	"github.com/fiorix/go-diameter/v4/diam/avp"
	"github.com/fiorix/go-diameter/v4/diam/datatype"
	"github.com/fiorix/go-diameter/v4/diam/dict"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func u(v uint32) *uint32 { return &v }

func wire(t *testing.T, m *diam.Message) *diam.Message {
	t.Helper()
	b, err := m.Serialize()
	require.NoError(t, err)
	out, err := diam.ReadMessage(bytes.NewReader(b), dict.Default)
	require.NoError(t, err)
	return out
}

// typeOf names the datatype of a decoded value as TS 29.212 does.
func typeOf(v datatype.Type) string {
	switch v.(type) {
	case datatype.Enumerated:
		return "Enumerated"
	case datatype.Unsigned32:
		return "Unsigned32"
	case datatype.Unsigned64:
		return "Unsigned64"
	case datatype.OctetString:
		return "OctetString"
	case datatype.UTF8String:
		return "UTF8String"
	case datatype.Time:
		return "Time"
	case datatype.Address:
		return "Address"
	case datatype.IPFilterRule:
		return "IPFilterRule"
	case *diam.GroupedAVP:
		return "Grouped"
	}
	return "other"
}

// checkGx walks every vendor-3GPP AVP of list: its code must be in TS
// 29.212 Table 5.3.0.1 or reused29212, its M bit must follow it, and a Gx
// AVP must decode with its type.
func checkGx(t *testing.T, path string, list []*diam.AVP) {
	t.Helper()
	for _, a := range list {
		if a.VendorID == vendorID3GPP {
			if spec, ok := spec29212ByCode[a.Code]; ok {
				assert.Equal(t, spec.MustM, a.Flags&avp.Mbit != 0, "%s/%d: M bit (TS 29.212 §%s)", path, a.Code, spec.Clause)
				assert.Equal(t, spec.Type, typeOf(a.Data), "%s/%d: type (TS 29.212 §%s)", path, a.Code, spec.Clause)
			} else if r, ok := reused29212[a.Code]; ok {
				assert.Equal(t, r.MustM, a.Flags&avp.Mbit != 0, "%s/%s: M bit (%s)", path, r.Name, r.Spec)
			} else {
				t.Errorf("%s: AVP %d is in neither TS 29.212 Table 5.3.0.1 nor reused29212", path, a.Code)
			}
		}
		if g, ok := a.Data.(*diam.GroupedAVP); ok {
			checkGx(t, path+"/"+strconv.FormatUint(uint64(a.Code), 10), g.AVP)
		}
	}
}

// TestDictionary_HasTS29212: every AVP of Table 5.3.0.1 resolves under Gx
// with its type. go-diameter's Gx dictionary lacked Charging-Rule-Report,
// Bearer-Operation, AN-GW-Status and others, and typed ToS-Traffic-Class as
// Unsigned32.
func TestDictionary_HasTS29212(t *testing.T) {
	for name, spec := range spec29212 {
		a, err := dict.Default.FindAVPWithVendor(AppIDGx, spec.Code, vendorID3GPP)
		if assert.NoError(t, err, name) {
			assert.Equal(t, spec.Type, a.Data.TypeName, name)
			assert.Equal(t, spec.MustM, a.Must == "M,V", name)
		}
	}
}

func fullRule() ChargingRule {
	return ChargingRule{
		ChargingRuleName:  "r1",
		ServiceIdentifier: 1, RatingGroup: 2,
		FlowDescription: []string{"permit out ip from any to assigned"},
		FlowInformation: []FlowInfo{{FlowDescription: "permit in ip from assigned to any", FlowDirection: u(3)}},
		FlowStatus:      u(0), ReportingLevel: u(0), Online: u(0), Offline: u(1), MeteringMethod: u(0),
		Precedence: 10, MonitoringKey: []byte("mk"),
		QoSInformation:      &QoSInformation{QoSClassIdentifier: 9},
		RedirectInformation: &RedirectInfo{RedirectSupport: u(1), RedirectAddressType: u(2), RedirectServerAddress: "http://portal"},
	}
}

// TestMessages_EncodeTS29212: a CCA, RAR and ASR built from filled inputs
// carry every 3GPP AVP with the code, type and M bit of TS 29.212. Before,
// Default-EPS-Bearer-QoS, Usage-Monitoring-Information, Monitoring-Key and
// the Allocation-Retention-Priority AVPs carried an M bit the table forbids.
func TestMessages_EncodeTS29212(t *testing.T) {
	tr := NewTranslator()
	arp := &AllocationRetentionPriority{PriorityLevel: 1, PreEmptionCapability: u(0), PreEmptionVulnerability: u(1)}
	qos := &QoSInformation{QoSClassIdentifier: 9, MaxRequestedBandwidthUL: 1, MaxRequestedBandwidthDL: 2,
		GuaranteedBitrateUL: 3, GuaranteedBitrateDL: 4, BearerIdentifier: []byte{5}, AllocationRetentionPriority: arp,
		APNAggregateMaxBitrateUL: 6, APNAggregateMaxBitrateDL: 7}
	umi := UsageMonitoringInfo{MonitoringKey: []byte("mk"), GrantedServiceUnit: &charging.ServiceUnit{CCTotalOctets: 1},
		UsageMonitoringLevel: u(0), UsageMonitoringReport: u(0), UsageMonitoringSupport: u(0)}
	install := ChargingRuleInstall{ChargingRuleName: []string{"n"}, ChargingRuleBaseName: []string{"b"},
		ChargingRuleDefinition: []ChargingRule{fullRule()}, BearerIdentifier: []byte{1},
		RuleActivationTime: 1792300000, RuleDeactivationTime: 1792400000}
	req := diam.NewRequest(272, AppIDGx, dict.Default)

	cca, err := tr.ToCCAMessage(&GxAnswer{
		ResultCode: 2001, CCRequestType: 1, SessionReleaseCause: u(0),
		ChargingRuleInstall: []ChargingRuleInstall{install},
		ChargingRuleRemove:  []ChargingRuleRemove{{ChargingRuleName: []string{"old"}}},
		QoSInformation:      qos, DefaultEPSBearerQoS: &DefaultEPSBearerQoS{QoSClassIdentifier: 9, AllocationRetentionPriority: arp},
		EventTrigger: []uint32{EventTriggerRAIChange}, RevalidationTime: 1792300000,
		UsageMonitoringInformation: []UsageMonitoringInfo{umi},
	}, req)
	require.NoError(t, err)
	rar := tr.CreateRAR("s", "pcef", "r", "gw", "r", &GxReAuthRequest{
		ChargingRuleInstall: []ChargingRuleInstall{install}, QoSInformation: qos,
		DefaultEPSBearerQoS: &DefaultEPSBearerQoS{QoSClassIdentifier: 9, AllocationRetentionPriority: arp},
		EventTrigger:        []uint32{1}, SessionReleaseCause: u(0),
	})
	asr := tr.CreateASR("s", "pcef", "r", "gw", "r", &GxAbortSessionRequest{AbortCause: 0})

	for name, m := range map[string]*diam.Message{"CCA": cca, "RAR": rar, "ASR": asr} {
		checkGx(t, name, wire(t, m).AVP)
	}
}

// TestCCA_ExperimentalResultAndAPNAMBR: a 3GPP result goes in
// Experimental-Result without Result-Code; apn_ambr goes into
// QoS-Information, TS 29.212 having no APN-AMBR AVP (1435 is S6a's).
func TestCCA_ExperimentalResultAndAPNAMBR(t *testing.T) {
	req := diam.NewRequest(272, AppIDGx, dict.Default)
	cca, err := NewTranslator().ToCCAMessage(&GxAnswer{
		ExperimentalResultCode: 5140, // DIAMETER_ERROR_INITIAL_PARAMETERS
		APNAMBR:                &APNAMBR{APNAggregateMaxBitrateUL: 10, APNAggregateMaxBitrateDL: 20},
	}, req)
	require.NoError(t, err)
	got := wire(t, cca)
	_, err = got.FindAVP(avp.ResultCode, 0)
	assert.Error(t, err, "no Result-Code beside Experimental-Result")
	_, err = got.FindAVP(avp.ExperimentalResult, 0)
	assert.NoError(t, err)
	for _, a := range got.AVP {
		assert.NotEqual(t, uint32(1435), a.Code, "no APN-AMBR AVP in Gx")
	}
	qos, err := got.FindAVP(avpQoSInformation, vendorID3GPP)
	require.NoError(t, err)
	q := NewTranslator().parseQoSInformation(qos)
	assert.Equal(t, uint32(10), q.APNAggregateMaxBitrateUL)
	assert.Equal(t, uint32(20), q.APNAggregateMaxBitrateDL)
}

// TestCCR_ChargingRuleReportAndANGWAddresses: a CCR's Charging-Rule-Report
// reaches the application, PCC-Rule-Status ACTIVE (0) included; it was
// always empty, its AVPs missing from the dictionary. Both AN-GW-Addresses
// arrive.
func TestCCR_ChargingRuleReportAndANGWAddresses(t *testing.T) {
	m := diam.NewRequest(272, AppIDGx, dict.Default)
	m.NewAVP(avp.SessionID, avp.Mbit, 0, datatype.UTF8String("pcef;1"))
	m.NewAVP(avpCCRequestType, avp.Mbit, 0, datatype.Enumerated(2))
	m.NewAVP(avpCCRequestNumber, avp.Mbit, 0, datatype.Unsigned32(1))
	m.AddAVP(avpGx(avpChargingRuleReport, &diam.GroupedAVP{AVP: []*diam.AVP{
		avpGx(avpChargingRuleName, datatype.OctetString("r1")),
		avpGx(avpPCCRuleStatus, datatype.Enumerated(0)),
		avpGx(avpRuleFailureCode, datatype.Enumerated(3)),
	}}))
	m.AddAVP(avpGx(avpANGWAddress, datatype.Address([]byte{10, 0, 0, 1})))
	m.AddAVP(avpGx(avpANGWAddress, datatype.Address([]byte{10, 0, 0, 2})))

	e, err := NewTranslator().ToCCREvent(wire(t, m), "t")
	require.NoError(t, err)
	require.Len(t, e.ChargingRuleReport, 1)
	r := e.ChargingRuleReport[0]
	assert.Equal(t, []string{"r1"}, r.ChargingRuleName)
	assert.Equal(t, u(0), r.PCCRuleStatus, "ACTIVE (0) is present")
	assert.Equal(t, uint32(3), r.RuleFailureCode)
	assert.Len(t, e.ANGWAddresses, 2)
	assert.Equal(t, e.ANGWAddresses[0], e.ANGWAddress)
}

// TestCRD_FlowInformationAndZeroEnums: Flow-Description goes inside
// Flow-Information (TS 29.212 §5.3.4), and Online/Offline are sent only when
// given, 0 included; Online and Offline were always sent, as 0 when absent.
func TestCRD_FlowInformationAndZeroEnums(t *testing.T) {
	g := NewTranslator().buildChargingRuleDefinition(&ChargingRule{ChargingRuleName: "r",
		FlowDescription: []string{"permit out ip from any to assigned"}, Online: u(0)})
	var flowInfo, online, offline int
	for _, a := range g.AVP {
		switch a.Code {
		case avpFlowDescription:
			t.Error("Flow-Description directly in Charging-Rule-Definition")
		case avpFlowInformation:
			flowInfo++
		case avpOnline:
			online++
			assert.Equal(t, datatype.Enumerated(0), a.Data)
		case avpOffline:
			offline++
		}
	}
	assert.Equal(t, 1, flowInfo)
	assert.Equal(t, 1, online, "DISABLE_ONLINE (0) is sent")
	assert.Zero(t, offline, "Offline was not given")
}

// TestFlowDirectionAndDefaults: Flow-Information carries a Flow-Direction
// whenever it carries a Flow-Description (TS 29.212 §5.3.53), taken from
// the description's direction when not given; ARP and Redirect-Information
// AVPs the application leaves out are not sent, so their defaults apply
// (§5.3.46, §5.3.47, §5.3.83) instead of value 0.
func TestFlowDirectionAndDefaults(t *testing.T) {
	dir := func(desc string) *uint32 { return flowDirection(FlowInfo{FlowDescription: desc}) }
	assert.Equal(t, u(1), dir("permit out ip from any to assigned"), "out: DOWNLINK")
	assert.Equal(t, u(2), dir("permit in ip from assigned to any"), "in: UPLINK")
	assert.Nil(t, dir(""))
	assert.Equal(t, u(3), flowDirection(FlowInfo{FlowDescription: "permit out ip from any to assigned", FlowDirection: u(3)}))

	arp := NewTranslator().buildARP(&AllocationRetentionPriority{PriorityLevel: 5})
	require.Len(t, arp.AVP, 1, "only Priority-Level")

	g := NewTranslator().buildChargingRuleDefinition(&ChargingRule{ChargingRuleName: "r",
		RedirectInformation: &RedirectInfo{RedirectServerAddress: "http://portal"}})
	for _, a := range g.AVP {
		if a.Code == avpRedirectInformation {
			for _, c := range a.Data.(*diam.GroupedAVP).AVP {
				assert.NotEqual(t, uint32(avpRedirectSupport), c.Code, "REDIRECTION_ENABLED is the default; not sent as 0")
			}
		}
	}
}
