// Copyright 2026 3eAI Labs Ltd
// SPDX-License-Identifier: AGPL-3.0-only

package diameter

import (
	"encoding/json"
	"testing"
	"time"

	pb "github.com/3eAI-Labs/diameter-ha-gateway-client"
	"github.com/fiorix/go-diameter/v4/diam"
	"github.com/fiorix/go-diameter/v4/diam/avp"
	"github.com/fiorix/go-diameter/v4/diam/datatype"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func grouped(code uint32, list ...*diam.AVP) *diam.AVP {
	return avp3GPP(code, &diam.GroupedAVP{AVP: list})
}

func u32(v uint32) *uint32 { return &v }

// TestToS6aEvent_FurtherULRAVPs: the ULR AVPs the gateway did not read reach
// the application from the wire, and the NOR field service_selection no
// longer takes the Active-APN's.
func TestToS6aEvent_FurtherULRAVPs(t *testing.T) {
	m := s6aRequest(316)
	m.AddAVP(grouped(avp.ActiveAPN,
		avp3GPP(avp.ContextIdentifier, datatype.Unsigned32(7)),
		diam.NewAVP(avp.ServiceSelection, avp.Mbit, 0, datatype.UTF8String("internet")),
		avp3GPP(avp.VisitedNetworkIdentifier, datatype.OctetString("mnc001.mcc001")),
		grouped(avp.SpecificAPNInfo,
			diam.NewAVP(avp.ServiceSelection, avp.Mbit, 0, datatype.UTF8String("ims")),
			diam.NewAVP(avp.MIP6AgentInfo, avp.Mbit, 0, &diam.GroupedAVP{AVP: []*diam.AVP{
				diam.NewAVP(avp.MIP6HomeLinkPrefix, avp.Mbit, 0, datatype.OctetString(append([]byte{64, 0x20, 0x01, 0x0d, 0xb8}, make([]byte, 12)...))),
			}}))))
	m.AddAVP(avp3GPP(avpSMSRegisterRequest, datatype.Enumerated(0)))
	m.AddAVP(avp3GPP(avpSGsMMEIdentity, datatype.UTF8String("mme1")))
	m.AddAVP(avp3GPP(avpCoupledNodeDiameterID, datatype.DiameterIdentity("sgsn.example")))
	m.AddAVP(grouped(avpAdjacentPLMNs,
		avp3GPP(avp.VisitedPLMNID, datatype.OctetString([]byte{0x00, 0xf1, 0x10})),
		avp3GPP(avp.VisitedPLMNID, datatype.OctetString([]byte{0x00, 0xf1, 0x20}))))
	m.AddAVP(grouped(avp.TerminalInformation, avp3GPP(avp3GPP2MEID, datatype.OctetString([]byte{0xab, 0xcd}))))

	e, err := NewTranslator().ToS6aEvent(fromWire(t, m), "t")
	require.NoError(t, err)
	require.Len(t, e.ActiveAPNs, 1)
	apn := e.ActiveAPNs[0]
	assert.Equal(t, uint32(7), apn.ContextID)
	assert.Equal(t, "internet", apn.ServiceSelection)
	assert.Equal(t, "mnc001.mcc001", apn.VisitedNetworkIdentifier)
	require.Len(t, apn.SpecificAPNInfo, 1)
	assert.Equal(t, "ims", apn.SpecificAPNInfo[0].ServiceSelection)
	assert.Equal(t, "4020010db8000000000000000000000000", apn.SpecificAPNInfo[0].MIP6AgentInfo.MIP6HomeLinkPrefix,
		"hex: length 64, prefix 2001:db8::")
	assert.Equal(t, []string{"internet"}, e.ActiveAPN, "active_apn keeps the names")
	assert.Empty(t, e.ServiceSelect, "the Active-APN's Service-Selection is not the NOR's")

	assert.Equal(t, u32(0), e.SMSRegisterRequest, "SMS_REGISTRATION_REQUIRED (0) is present")
	assert.Equal(t, "mme1", e.SGsMMEIdentity)
	assert.Equal(t, "sgsn.example", e.CoupledNodeDiameterID)
	assert.Equal(t, []string{"00f110", "00f120"}, e.AdjacentPLMNs)
	assert.Equal(t, "abcd", e.TerminalInfo.MEID)
}

// TestToS6aEvent_PURAndNOR: a PUR's EPS-Location-Information and a NOR's
// Maximum-UE-Availability-Time and Visited-Network-Identifier.
func TestToS6aEvent_PURAndNOR(t *testing.T) {
	pur := s6aRequest(321)
	pur.AddAVP(grouped(avpEPSLocationInformation,
		grouped(avpMMELocationInformation,
			avp3GPP(avpTrackingAreaIdentity, datatype.OctetString([]byte{0x00, 0xf1, 0x10, 0x00, 0x01})),
			avp3GPP(avpAgeOfLocationInformation, datatype.Unsigned32(5)))))
	e, err := NewTranslator().ToS6aEvent(fromWire(t, pur), "t")
	require.NoError(t, err)
	require.NotNil(t, e.EPSLocationInformation)
	assert.Equal(t, "00f1100001", e.EPSLocationInformation.MME.TrackingAreaIdentity)
	assert.Equal(t, u32(5), e.EPSLocationInformation.MME.AgeOfLocationInformation)

	at := time.Date(2026, 10, 18, 12, 0, 0, 0, time.UTC)
	nor := s6aRequest(323)
	nor.AddAVP(avp3GPP(avpMaxUEAvailabilityTime, datatype.Time(at)))
	nor.AddAVP(avp3GPP(avp.VisitedNetworkIdentifier, datatype.OctetString("mnc001.mcc001")))
	e, err = NewTranslator().ToS6aEvent(fromWire(t, nor), "t")
	require.NoError(t, err)
	require.NotNil(t, e.MaximumUEAvailabilityTime)
	assert.True(t, at.Equal(*e.MaximumUEAvailabilityTime))
	assert.Equal(t, "mnc001.mcc001", e.VisitedNetworkIdentifier)
}

// TestDecodeMMEAnswer_IDA: the application that sent an IDR receives the
// MME's answer whole; it received only the result code.
func TestDecodeMMEAnswer_IDA(t *testing.T) {
	seen := time.Date(2026, 10, 18, 11, 30, 0, 0, time.UTC)
	ida := diam.NewMessage(319, diam.ProxiableFlag, 16777251, 1, 2, nil)
	ida.NewAVP(avp.SessionID, avp.Mbit, 0, datatype.UTF8String("hss;1;2;id"))
	ida.NewAVP(avp.ResultCode, avp.Mbit, 0, datatype.Unsigned32(diam.Success))
	ida.AddAVP(diam.NewAVP(avp.SupportedFeatures, avp.Vbit, vendorID3GPP, &diam.GroupedAVP{AVP: []*diam.AVP{
		diam.NewAVP(avp.VendorID, avp.Mbit, 0, datatype.Unsigned32(10415)),
		avp3GPP(avp.FeatureListID, datatype.Unsigned32(1)),
		avp3GPP(avp.FeatureList, datatype.Unsigned32(3)),
	}}))
	ida.AddAVP(avp3GPP(avpIMSVoiceOverPS, datatype.Enumerated(1)))
	ida.AddAVP(avp3GPP(avpLastUEActivityTime, datatype.Time(seen)))
	ida.AddAVP(avp3GPP(avp.RATType, datatype.Enumerated(1004)))
	ida.AddAVP(avp3GPP(avpIDAFlags, datatype.Unsigned32(1)))
	ida.AddAVP(grouped(avpEPSUserState, grouped(avpMMEUserState, avp3GPP(avpUserState, datatype.Enumerated(0)))))
	ida.AddAVP(grouped(avpEPSLocationInformation, grouped(avpMMELocationInformation,
		avp3GPP(avpEUTRANCellGlobalIdentity, datatype.OctetString([]byte{0x00, 0xf1, 0x10, 0x01, 0x02, 0x03, 0x04})),
		avp3GPP(avpCurrentLocationRetrieved, datatype.Enumerated(0)))))
	ida.AddAVP(grouped(avpLocalTimeZone,
		avp3GPP(avpTimeZone, datatype.UTF8String("+12")),
		avp3GPP(avpDaylightSavingTime, datatype.Enumerated(0))))

	a := DecodeMMEAnswer(fromWire(t, ida))
	assert.Equal(t, uint32(diam.Success), a.ResultCode)
	assert.Equal(t, []pb.SupportedFeature{{VendorID: 10415, FeatureListID: 1, FeatureList: 3}}, a.SupportedFeatures)
	assert.Equal(t, u32(1), a.IMSVoiceOverPSSessionsSupported)
	require.NotNil(t, a.LastUEActivityTime)
	assert.True(t, seen.Equal(*a.LastUEActivityTime))
	assert.Equal(t, uint32(1004), a.RATType)
	assert.Equal(t, uint32(1), a.IDAFlags)
	assert.Equal(t, u32(0), a.EPSUserState.MMEUserState, "DETACHED (0) is present")
	assert.Equal(t, "00f11001020304", a.EPSLocationInformation.MME.EUTRANCellGlobalIdentity)
	assert.Equal(t, u32(0), a.EPSLocationInformation.MME.CurrentLocationRetrieved)
	assert.Equal(t, &pb.LocalTimeZone{TimeZone: "+12"}, a.LocalTimeZone)

	body, err := json.Marshal(a)
	require.NoError(t, err)
	assert.Contains(t, string(body), `"eps_user_state":{"mme_user_state":0}`)
}

// TestAIA_UTRANAndGERANVectors: an AIA carries UTRAN and GERAN vectors
// beside E-UTRAN ones (TS 29.272 §7.3.17); only E-UTRAN vectors were sent.
func TestAIA_UTRANAndGERANVectors(t *testing.T) {
	reply := `{"result_code":2001,"ue_usage_type":5,
		"utran_vectors":[{"item":1,"rand":"00","xres":"01","autn":"02","ck":"03","ik":"04"}],
		"geran_vectors":[{"item":2,"rand":"05","sres":"06","kc":"07"}]}`
	ans, err := NewTranslator().ToDiameterAnswer([]byte(reply), s6aRequest(318))
	require.NoError(t, err)
	got := fromWire(t, ans)

	info, err := got.FindAVP(avp.AuthenticationInfo, vendorID3GPP)
	require.NoError(t, err)
	vectors := children(info)
	utran := children(child(vectors, avp.UTRANVector, vendorID3GPP))
	geran := children(child(vectors, avp.GERANVector, vendorID3GPP))
	assert.Equal(t, "03", hexOf(child(utran, avpConfidentialityKey, vendorID3GPP)))
	assert.Equal(t, "04", hexOf(child(utran, avpIntegrityKey, vendorID3GPP)))
	assert.Equal(t, "07", hexOf(child(geran, avp.Kc, vendorID3GPP)))
	assert.Equal(t, uint32(5), u32Value(child(got.AVP, avpUEUsageType, vendorID3GPP)))

	_, err = NewTranslator().ToDiameterAnswer([]byte(`{"result_code":2001,"utran_vectors":[{"rand":"zz"}]}`), s6aRequest(318))
	assert.Error(t, err, "invalid hex is refused, not sent")
}

// TestApplyRequestExtras: the fields an application adds to its requests,
// and invalid hex refused before anything is sent.
func TestApplyRequestExtras(t *testing.T) {
	tr := NewTranslator()
	rsr, _ := tr.CreateRSR("00101", "mme", "r", "hss", "r")
	require.NoError(t, ApplyRequestExtras(rsr, "RSR", &pb.S6aEvent{UserIDs: []string{"00102", "00103"}, ResetIDs: []string{"aa"}, DRMP: 3}))
	got := fromWire(t, rsr)
	var users []string
	for _, a := range got.AVP {
		if a.Code == avpUserID {
			users = append(users, AVPString(a))
		}
	}
	assert.Equal(t, []string{"00101", "00102", "00103"}, users, "*[ User-Id ] (§7.2.15)")
	assert.Equal(t, "aa", hexOf(child(got.AVP, avpResetID, vendorID3GPP)))
	drmp := child(got.AVP, avp.DRMP, 0)
	require.NotNil(t, drmp)
	assert.Zero(t, drmp.Flags&avp.Mbit)

	clr, _ := tr.CreateCLR("001010000000001", "mme", "r", "hss", "r", 0)
	require.NoError(t, ApplyRequestExtras(clr, "CLR", &pb.S6aEvent{CLRFlags: 1}))
	assert.Equal(t, uint32(1), u32Value(child(fromWire(t, clr).AVP, avpCLRFlags, vendorID3GPP)))

	assert.Error(t, ValidateRequestExtras("DSR", &pb.S6aEvent{TSCodes: []string{"x"}}))
	assert.Error(t, ValidateRequestExtras("IDR", &pb.S6aEvent{ResetIDs: []string{""}}), "an empty value is not sent")
}
