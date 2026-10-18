// Copyright 2026 3eAI Labs Ltd
// SPDX-License-Identifier: AGPL-3.0-only

package diameter

import (
	"encoding/json"
	"reflect"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"testing"

	pb "github.com/3eAI-Labs/diameter-ha-gateway-client"
	"github.com/fiorix/go-diameter/v4/diam"
	"github.com/fiorix/go-diameter/v4/diam/avp"
	"github.com/fiorix/go-diameter/v4/diam/datatype"
	"github.com/fiorix/go-diameter/v4/diam/dict"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

var specByCode = func() map[uint32]string {
	m := make(map[uint32]string, len(spec29272))
	for name, a := range spec29272 {
		m[a.Code] = name
	}
	return m
}()

var dictAVPRe = regexp.MustCompile(`<avp name="([^"]+)" code="(\d+)" must="([^"]*)"[^>]*vendor-id="10415">\s*<data type="(\w+)"`)

// TestDictionary_MatchesTS29272 checks every 3GPP AVP the gateway adds to
// go-diameter's dictionary against TS 29.272: code, type and M-bit rule from
// Table 7.3.1/1, or the M-bit rule Table 7.3.1/2 sets for a re-used AVP. The
// S6a tests passed with wrong codes because encoder and decoder shared this
// dictionary.
func TestDictionary_MatchesTS29272(t *testing.T) {
	for file, xml := range map[string]string{
		"dict_extensions.go":       idrDictXML,
		"dict_extensions_29272.go": dict29272XML,
		"dict_extensions_29336.go": dict29336XML,
		"dict_extensions_s13.go":   s13DictXML,
	} {
		for _, m := range dictAVPRe.FindAllStringSubmatch(xml, -1) {
			name, must, typ := m[1], m[3], m[4]
			code64, _ := strconv.ParseUint(m[2], 10, 32)
			code := uint32(code64)
			spec, ok := spec29272[name]
			if !ok {
				r, reused := reused29272[code]
				if !reused {
					t.Errorf("%s: %s (%d) is in neither TS 29.272 Table 7.3.1/1 nor reused29272", file, name, code)
				} else if r.Name != name {
					t.Errorf("%s: %d is %s in reused29272, %s in the dictionary", file, code, r.Name, name)
				} else if r.Table72 {
					assert.Equal(t, r.MustM, strings.Contains(must, "M"), "%s: %s M-bit rule (TS 29.272 Table 7.3.1/2)", file, name)
				}
				continue
			}
			assert.Equal(t, spec.Code, code, "%s: %s code (TS 29.272 §%s)", file, name, spec.Clause)
			assert.Equal(t, wireClass(spec.Type), wireClass(typ), "%s: %s type %s (TS 29.272 §%s: %s)", file, name, typ, spec.Clause, spec.Type)
			assert.Equal(t, spec.MustM, strings.Contains(must, "M"), "%s: %s M-bit rule (TS 29.272 §%s)", file, name, spec.Clause)
		}
	}
}

// TestSubscriptionData_EncodesTS29272 fills every field of a
// SubscriptionProfile, encodes it, and checks every 3GPP AVP in the result
// against TS 29.272 (checkTS29272).
func TestSubscriptionData_EncodesTS29272(t *testing.T) {
	p := &pb.SubscriptionProfile{}
	fill(reflect.ValueOf(p).Elem(), "", 0)
	g := encodeSubscriptionData(p)
	require.NotNil(t, g)

	seen := checkTS29272(t, "Subscription-Data", g.AVP)
	codes := make([]int, 0, len(seen))
	for c := range seen {
		codes = append(codes, int(c))
	}
	sort.Ints(codes)
	t.Logf("%d distinct 3GPP AVPs encoded: %v", len(codes), codes)
	assert.Greater(t, len(codes), 60, "the filled profile should exercise most of the encoder")
}

// TestMessages_EncodeTS29272 builds every S6a and S13 message the gateway
// sends, from fully filled inputs, and checks their AVPs the same way.
func TestMessages_EncodeTS29272(t *testing.T) {
	tr := NewTranslator()
	profile := &pb.SubscriptionProfile{}
	fill(reflect.ValueOf(profile).Elem(), "", 0)

	answer := func(cmd, app uint32, res any) *diam.Message {
		fill(reflect.ValueOf(res).Elem(), "", 0)
		body, err := json.Marshal(res)
		require.NoError(t, err)
		req := diam.NewRequest(cmd, app, dict.Default)
		req.NewAVP(avp.SessionID, avp.Mbit, 0, datatype.UTF8String("mme.example;1;1"))
		req.NewAVP(avp.OriginHost, avp.Mbit, 0, datatype.DiameterIdentity("mme.example"))
		req.NewAVP(avp.OriginRealm, avp.Mbit, 0, datatype.DiameterIdentity("example"))
		req.NewAVP(avp.AuthSessionState, avp.Mbit, 0, datatype.Enumerated(1))
		req.NewAVP(avp.DRMP, 0, 0, datatype.Enumerated(1))
		ans, err := tr.ToDiameterAnswer(body, req)
		require.NoError(t, err)
		return ans
	}
	idr, _ := tr.CreateIDR("001010123456789", "mme.example", "example", "hss.example", "example", profile, "", 1)
	dsr, _ := tr.CreateDSR("001010123456789", "mme.example", "example", "hss.example", "example", 1, []uint32{1})
	clr, _ := tr.CreateCLR("001010123456789", "mme.example", "example", "hss.example", "example", 0)
	rsr, _ := tr.CreateRSR("001010123456789", "mme.example", "example", "hss.example", "example")
	// The fields an application may add to its requests, all filled.
	event := &pb.S6aEvent{}
	fill(reflect.ValueOf(event).Elem(), "", 0)
	for cmd, m := range map[string]*diam.Message{"IDR": idr, "DSR": dsr, "CLR": clr, "RSR": rsr} {
		require.NoError(t, ApplyRequestExtras(m, cmd, event), cmd)
	}

	for name, m := range map[string]*diam.Message{
		"ULA": answer(316, 16777251, &pb.S6aAnswer{}),
		"AIA": answer(318, 16777251, &pb.S6aAnswer{}),
		"PUA": answer(321, 16777251, &pb.S6aAnswer{}),
		"NOA": answer(323, 16777251, &pb.S6aAnswer{}),
		"ECA": answer(324, AppIDS13, &pb.S13Answer{}),
		"IDR": idr,
		"DSR": dsr,
		"CLR": clr,
		"RSR": rsr,
	} {
		checkTS29272(t, name, m.AVP)
	}
}

// TestCodeConstants_NameTS29272AVPs ties every AVP code the encoder uses to the
// AVP TS 29.272 names: a code swapped for another AVP of the same type and
// flags (E-UTRAN-Cell-Global-Identity sent as 1605, Routing-Area-Identity)
// passes every check on the encoded message. go-diameter's constants are
// listed too: they come from its dictionary, not from the specification.
func TestCodeConstants_NameTS29272AVPs(t *testing.T) {
	for name, code := range map[string]uint32{
		"A-MSISDN":      avpAMSISDN,
		"STN-SR":        avpSTNSR,
		"ICS-Indicator": avpICSIndicator,
		"Roaming-Restricted-Due-To-Unsupported-Feature": avpRoamingRestrictedDueToUnsupp,
		"Subscribed-Periodic-RAU-TAU-Timer":             avpSubscribedPeriodicRAUTAUTimer,
		"Relay-Node-Indicator":                          avpRelayNodeIndicator,
		"MDT-User-Consent":                              avpMDTUserConsent,
		"Subscribed-VSRVCC":                             avpSubscribedVSRVCC,
		"ProSe-Subscription-Data":                       avpProSeSubscriptionData,
		"ProSe-Permission":                              avpProSePermission,
		"Subscription-Data-Flags":                       avpSubscriptionDataFlags,
		"Pre-emption-Capability":                        avpPreEmptionCapability,
		"Pre-emption-Vulnerability":                     avpPreEmptionVulnerability,
		"Teleservice-List":                              avpTeleserviceList,
		"TS-Code":                                       avpTSCode,
		"CSG-Id":                                        avpCSGId,
		"Expiration-Date":                               avpExpirationDate,
		"GMLC-Number":                                   avpGMLCNumber,
		"LCS-PrivacyException":                          avpLCSPrivacyExceptionList,
		"SS-Code":                                       avpSSCode,
		"SS-Status":                                     avpSSStatus,
		"Notification-To-UE-User":                       avpNotificationToUEUser,
		"MO-LR":                                         avpMOLR,
		"Adjacent-Access-Restriction-Data":              avpAdjacentAccessRestrictionData,
		"IMSI-Group-Id":                                 avpIMSIGroupID,
		"Group-Service-Id":                              avpGroupServiceID,
		"Group-PLMN-Id":                                 avpGroupPLMNID,
		"Local-Group-Id":                                avpLocalGroupID,
		"Restoration-Priority":                          avpRestorationPriority,
		"SIPTO-Local-Network-Permission":                avpSIPTOLocalNetworkPermission,
		"Emergency-Info":                                avpEmergencyInfo,
		"V2X-Subscription-Data":                         avpV2XSubscriptionData,
		"V2X-Permission":                                avpV2XPermission,
		"eDRX-Cycle-Length":                             avpEDRXCycleLength,
		"eDRX-Cycle-Length-Value":                       avpEDRXCycleLengthVal,
		"Paging-Time-Window":                            avpPagingTimeWindow,
		"Operation-Mode":                                avpOperationMode,
		"Paging-Time-Window-Length":                     avpPagingTimeWindowLen,
		"SCEF-ID":                                       avpSCEFID,
		"WLAN-offloadability":                           avpWLANOffloadability,
		"WLAN-offloadability-EUTRAN":                    avpWLANOffloadabilityEUTRAN,
		"WLAN-offloadability-UTRAN":                     avpWLANOffloadabilityUTRAN,
		"UE-Usage-Type":                                 avpUEUsageType,
		"DL-Buffering-Suggested-Packet-Count":           avpDLBufferingSuggestedPacketCount,
		"Service-Gap-Time":                              avpServiceGapTime,
		"Non-IP-PDN-Type-Indicator":                     avpNonIPPDNTypeIndicator,
		"Non-IP-Data-Delivery-Mechanism":                avpNonIPDataDeliveryMechanism,
		"SCEF-Realm":                                    avpSCEFRealm,
		"Preferred-Data-Mode":                           avpPreferredDataMode,
		"PDN-Connection-Continuity":                     avpPDNConnectionContinuity,
		"Job-Type":                                      avpJobType,
		"Area-Scope":                                    avpAreaScope,
		"List-Of-Measurements":                          avpListOfMeasurements,
		"Reporting-Trigger":                             avpReportingTrigger,
		"Report-Interval":                               avpReportInterval,
		"Report-Amount":                                 avpReportAmount,
		"Event-Threshold-RSRP":                          avpEventThresholdRSRP,
		"Event-Threshold-RSRQ":                          avpEventThresholdRSRQ,
		"Logging-Interval":                              avpLoggingInterval,
		"Logging-Duration":                              avpLoggingDuration,
		"Cell-Global-Identity":                          avpCellGlobalIdentity,
		"E-UTRAN-Cell-Global-Identity":                  avpEUTRANCellGlobalIdentity,
		"Tracking-Area-Identity":                        avpTrackingAreaIdentity,
		"Routing-Area-Identity":                         avpRoutingAreaIdentity,
		"Location-Area-Identity":                        avpLocationAreaIdentity,
		"Measurement-Period-LTE":                        avpMeasurementPeriodLTE,
		"Measurement-Period-UMTS":                       avpMeasurementPeriodUMTS,
		"Collection-Period-RRM-LTE":                     avpCollectionPeriodRRMLTE,
		"Collection-Period-RRM-UMTS":                    avpCollectionPeriodRRMUMTS,
		"Positioning-Method":                            avpPositioningMethod,
		"Measurement-Quantity":                          avpMeasurementQuantity,
		"Event-Threshold-Event-1F":                      avpEventThresholdEvent1F,
		"Event-Threshold-Event-1I":                      avpEventThresholdEvent1I,
		"Trace-Reference":                               avpTraceReference,
		"Subscription-Data":                             avp.SubscriptionData,
		"Subscriber-Status":                             avp.SubscriberStatus,
		"MSISDN":                                        avp.MSISDN,
		"Network-Access-Mode":                           avp.NetworkAccessMode,
		"Operator-Determined-Barring":                   avp.OperatorDeterminedBarring,
		"HPLMN-ODB":                                     avp.HPLMNODB,
		"Regional-Subscription-Zone-Code":               avp.RegionalSubscriptionZoneCode,
		"Access-Restriction-Data":                       avp.AccessRestrictionData,
		"APN-OI-Replacement":                            avp.APNOIReplacement,
		"LCS-Info":                                      avp.LCSInfo,
		"Call-Barring-Info":                             avp.CallBarringInfo,
		"3GPP-Charging-Characteristics":                 avp.TGPPChargingCharacteristics,
		"AMBR":                                          avp.AMBR,
		"APN-Configuration-Profile":                     avp.APNConfigurationProfile,
		"GPRS-Subscription-Data":                        avp.GPRSSubscriptionData,
		"RAT-Frequency-Selection-Priority-ID":           avp.RATFrequencySelectionPriorityID,
		"CSG-Subscription-Data":                         avp.CSGSubscriptionData,
		"MPS-Priority":                                  avp.MPSPriority,
		"VPLMN-LIPA-Allowed":                            avp.VPLMNLIPAAllowed,
		"Trace-Data":                                    avp.TraceData,
		"Context-Identifier":                            avp.ContextIdentifier,
		"All-APN-Configurations-Included-Indicator":     avp.AllAPNConfigurationsIncludedIndicator,
		"APN-Configuration":                             avp.APNConfiguration,
		"PDN-Type":                                      avp.PDNType,
		"EPS-Subscribed-QoS-Profile":                    avp.EPSSubscribedQoSProfile,
		"VPLMN-Dynamic-Address-Allowed":                 avp.VPLMNDynamicAddressAllowed,
		"Visited-Network-Identifier":                    avp.VisitedNetworkIdentifier,
		"PDN-GW-Allocation-Type":                        avp.PDNGWAllocationType,
		"Specific-APN-Info":                             avp.SpecificAPNInfo,
		"Served-Party-IP-Address":                       avp.ServedPartyIPAddress,
		"SIPTO-Permission":                              avp.SIPTOPermission,
		"LIPA-Permission":                               avp.LIPAPermission,
		"QoS-Class-Identifier":                          avp.QoSClassIdentifier,
		"Allocation-Retention-Priority":                 avp.AllocationRetentionPriority,
		"Priority-Level":                                avp.PriorityLevel,
		"External-Client":                               avp.ExternalClient,
		"PLMN-Client":                                   avp.PLMNClient,
		"Service-Type":                                  avp.TGPPServiceType,
		"ServiceTypeIdentity":                           avp.ServiceTypeIdentity,
		"Client-Identity":                               avp.ClientIdentity,
		"GMLC-Restriction":                              avp.GMLCRestriction,
		"Trace-Depth":                                   avp.TraceDepth,
		"Trace-NE-Type-List":                            avp.TraceNETypeList,
		"Trace-Interface-List":                          avp.TraceInterfaceList,
		"Trace-Event-List":                              avp.TraceEventList,
		"OMC-Id":                                        avp.OMCID,
		"Trace-Collection-Entity":                       avp.TraceCollectionEntity,
		"MDT-Configuration":                             avp.MDTConfiguration,
		"Complete-Data-List-Included-Indicator":         avp.CompleteDataListIncludedIndicator,
		"PDP-Context":                                   avp.PDPContext,
		"PDP-Type":                                      avp.PDPType,
		"QoS-Subscribed":                                avp.QoSSubscribed,
		"Ext-PDP-Type":                                  avp.ExtPDPType,
		"Ext-PDP-Address":                               avp.ExtPDPAddress,
		"PDP-Address":                                   avp.PDPAddress,
		"Max-Requested-Bandwidth-UL":                    avp.MaxRequestedBandwidthUL,
		"Max-Requested-Bandwidth-DL":                    avp.MaxRequestedBandwidthDL,
		"Extended-Max-Requested-BW-UL":                  avp.ExtendedMaxRequestedBWUL,
		"Extended-Max-Requested-BW-DL":                  avp.ExtendedMaxRequestedBWDL,
		"Visited-PLMN-Id":                               avp.VisitedPLMNID,
		"Cancellation-Type":                             avp.CancellationType,
		"ULA-Flags":                                     avp.ULAFlags,
		"PUA-Flags":                                     avp.PUAFlags,
		"Authentication-Info":                           avp.AuthenticationInfo,
		"E-UTRAN-Vector":                                avp.EUTRANVector,
		"Item-Number":                                   avp.ItemNumber,
		"RAND":                                          avp.RAND,
		"XRES":                                          avp.XRES,
		"AUTN":                                          avp.AUTN,
		"KASME":                                         avp.KASME,
		"Supported-Features":                            avp.SupportedFeatures,
		"Feature-List-ID":                               avp.FeatureListID,
		"Feature-List":                                  avp.FeatureList,
		"RAT-Type":                                      avp.RATType,
	} {
		if spec, ok := spec29272[name]; ok {
			assert.Equal(t, spec.Code, code, "%s (TS 29.272 §%s)", name, spec.Clause)
		} else if r, ok := reused29272[code]; !ok || r.Name != name {
			t.Errorf("%s (%d) is in neither TS 29.272 Table 7.3.1/1 nor reused29272", name, code)
		}
	}
}

// checkTS29272 walks avps and checks each vendor-10415 AVP: the code must be
// one TS 29.272 defines (Table 7.3.1/1) or re-uses (reused29272), the M-bit
// must follow the specification's rule, and for the AVPs TS 29.272 defines
// the data type must match. Base AVPs whose M-bit Table 7.3.1/2 sets are
// checked too. It returns the 3GPP codes seen.
func checkTS29272(t *testing.T, path string, avps []*diam.AVP) map[uint32]bool {
	t.Helper()
	seen := map[uint32]bool{}
	var walk func(path string, avps []*diam.AVP)
	walk = func(path string, avps []*diam.AVP) {
		for _, a := range avps {
			// The Vendor-Id field is on the wire only with the V-bit: an AVP
			// held with vendor 10415 but sent without V is a vendor-0 AVP.
			switch {
			case a.VendorID == vendorID3GPP && a.Flags&avp.Vbit != 0:
				seen[a.Code] = true
				checkEncoded(t, path, a)
				// The S6a dictionary must resolve it, or the gateway cannot
				// read back what it writes (and a Grouped decodes as bytes).
				_, err := dict.Default.FindAVPWithVendor(16777251, a.Code, vendorID3GPP)
				assert.NoError(t, err, "%s/%s (%d): not in the S6a dictionary", path, codeName(a.Code), a.Code)
			case a.VendorID == vendorID3GPP:
				t.Errorf("%s: %s (%d) sent without the V-bit", path, codeName(a.Code), a.Code)
			case a.VendorID == 0 && a.Code == avp.DRMP:
				assert.Zero(t, a.Flags&avp.Mbit, "%s/DRMP: M-bit (TS 29.272 Table 7.3.1/2: must not set)", path)
			}
			if sub, ok := a.Data.(*diam.GroupedAVP); ok {
				walk(path+"/"+codeName(a.Code), sub.AVP)
			}
		}
	}
	walk(path, avps)
	return seen
}

func checkEncoded(t *testing.T, path string, a *diam.AVP) {
	t.Helper()
	if spec, ok := spec29272ByCode[a.Code]; ok {
		name := specByCode[a.Code]
		assert.Equal(t, spec.MustM, a.Flags&avp.Mbit != 0, "%s/%s (%d): M-bit (TS 29.272 §%s)", path, name, a.Code, spec.Clause)
		assert.Equal(t, wireClass(spec.Type), wireClass(dataType(a.Data)), "%s/%s (%d): type %s (TS 29.272 §%s: %s)", path, name, a.Code, dataType(a.Data), spec.Clause, spec.Type)
		return
	}
	r, ok := reused29272[a.Code]
	if !ok {
		t.Errorf("%s: AVP %d is in neither TS 29.272 Table 7.3.1/1 nor reused29272", path, a.Code)
		return
	}
	mustM := r.MustM
	if a.Code == avp.SupportedFeatures && isAnswerPath(path) {
		// TS 29.229 §7.2.1: within an answer the AVP "shall never have the
		// 'M' bit set" (in a request it always has).
		mustM = false
	}
	assert.Equal(t, mustM, a.Flags&avp.Mbit != 0, "%s/%s (%d): M-bit (%s)", path, r.Name, a.Code, r.Spec)
}

// isAnswerPath reports whether path, "ULA/…" or "IDR/…", walks an answer.
func isAnswerPath(path string) bool {
	return strings.HasSuffix(strings.SplitN(path, "/", 2)[0], "A")
}

func codeName(code uint32) string {
	if n, ok := specByCode[code]; ok {
		return n
	}
	if r, ok := reused29272[code]; ok {
		return r.Name
	}
	return "avp" + strconv.Itoa(int(code))
}

// wireClass groups the types that encode to the same octets: the 32-bit
// integers, and the octet strings. A mismatch inside a class is invisible on
// the wire; across classes the peer decodes something else.
func wireClass(typ string) string {
	switch typ {
	case "Unsigned32", "Enumerated", "Integer32":
		return "32-bit integer"
	case "OctetString", "UTF8String", "DiameterIdentity", "DiameterURI":
		return "octets"
	}
	return typ
}

func dataType(d datatype.Type) string {
	switch d.(type) {
	case *diam.GroupedAVP:
		return "Grouped"
	case datatype.Unsigned32:
		return "Unsigned32"
	case datatype.Unsigned64:
		return "Unsigned64"
	case datatype.Integer32:
		return "Integer32"
	case datatype.Enumerated:
		return "Enumerated"
	case datatype.OctetString:
		return "OctetString"
	case datatype.UTF8String:
		return "UTF8String"
	case datatype.DiameterIdentity:
		return "DiameterIdentity"
	case datatype.Address:
		return "Address"
	case datatype.Time:
		return "Time"
	}
	return reflect.TypeOf(d).Name()
}

// fill sets every field of v to a non-zero value the encoder accepts: IP
// addresses for fields named like addresses, hex digits for other strings.
func fill(v reflect.Value, name string, depth int) {
	if depth > 8 {
		return
	}
	switch v.Kind() {
	case reflect.Ptr:
		v.Set(reflect.New(v.Type().Elem()))
		fill(v.Elem(), name, depth+1)
	case reflect.Struct:
		for i := 0; i < v.NumField(); i++ {
			if v.Type().Field(i).IsExported() {
				fill(v.Field(i), v.Type().Field(i).Name, depth+1)
			}
		}
	case reflect.Slice:
		s := reflect.MakeSlice(v.Type(), 1, 1)
		fill(s.Index(0), name, depth+1)
		v.Set(s)
	case reflect.String:
		if strings.Contains(name, "Address") || strings.HasSuffix(name, "IP") || strings.Contains(name, "IPAddress") {
			v.SetString("192.0.2.1")
		} else {
			v.SetString("01")
		}
	case reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64, reflect.Uint:
		v.SetUint(1)
	case reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64, reflect.Int:
		v.SetInt(1)
	case reflect.Bool:
		v.SetBool(true)
	}
}
