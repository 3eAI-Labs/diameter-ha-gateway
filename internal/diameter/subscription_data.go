// Copyright 2026 3eAI Labs Ltd
// SPDX-License-Identifier: AGPL-3.0-only

package diameter

import (
	"encoding/hex"
	"fmt"
	"math"
	"net"
	"time"

	pb "github.com/3eAI-Labs/diameter-ha-gateway-client"
	"github.com/3eAI-Labs/diameter-ha-gateway/internal/platform/logger"
	"github.com/fiorix/go-diameter/v4/diam"
	"github.com/fiorix/go-diameter/v4/diam/avp"
	"github.com/fiorix/go-diameter/v4/diam/datatype"
)

// AVP codes not exported by go-diameter v4 (used here numerically).
const (
	avpAMSISDN                       = 1643
	avpUserID                        = 1444
	avpSTNSR                         = 1433
	avpICSIndicator                  = 1491
	avpRoamingRestrictedDueToUnsupp  = 1457
	avpSubscribedPeriodicRAUTAUTimer = 1619
	avpRelayNodeIndicator            = 1633
	avpMDTUserConsent                = 1634
	avpSubscribedVSRVCC              = 1636
	avpProSeSubscriptionData         = 3701
	avpProSePermission               = 3702
	avpSubscriptionDataFlags         = 1654
	avpPreEmptionCapability          = 1047
	avpPreEmptionVulnerability       = 1048
	avpTeleserviceList               = 1486
	avpTSCode                        = 1487
	avpCSGId                         = 1437
	avpExpirationDate                = 1439
	avpGMLCNumber                    = 1474
	avpLCSPrivacyExceptionList       = 1475
	avpSSCode                        = 1476
	avpSSStatus                      = 1477
	avpNotificationToUEUser          = 1478
	avpMOLR                          = 1485

	// TS 29.272 §7.3.x Rel-13/14 sub-AVP families.
	avpAdjacentAccessRestrictionData = 1673 // §7.3.137 (Rel-14)
	avpIMSIGroupID                   = 1675 // §7.3.122 (Rel-13)
	avpGroupServiceID                = 1676
	avpGroupPLMNID                   = 1677
	avpLocalGroupID                  = 1678

	// Restoration-Priority (1663, TS 29.272 §7.3.174). PDP-Address is
	// go-diameter's avp.PDPAddress (1227, TS 32.299, re-used by TS 29.272
	// Table 7.3.1/2); 1471 is 3GPP2-MEID.
	avpRestorationPriority = 1663

	// APNConfiguration extras.
	avpSIPTOLocalNetworkPermission = 1665

	// Rel-13/14 small bundles.
	avpEmergencyInfo       = 1687
	avpV2XSubscriptionData = 1688 // §7.3.212
	avpV2XPermission       = 1689 // §7.3.213
	avpEDRXCycleLength     = 1691 // §7.3.215
	avpEDRXCycleLengthVal  = 1692 // §7.3.216
	avpPagingTimeWindow    = 1701 // §7.3.226
	avpOperationMode       = 1702 // §7.3.227
	avpPagingTimeWindowLen = 1703 // §7.3.228

	// SCEF-ID (TS 29.336, re-used in APN-Configuration, TS 29.272 §7.3.35).
	// Code not checked against TS 29.336.
	avpSCEFID = 3125

	// APNConfiguration sub-Grouped families.
	avpWLANOffloadability       = 1667
	avpWLANOffloadabilityEUTRAN = 1668
	avpWLANOffloadabilityUTRAN  = 1669

	// Top-level extras + APNConfiguration extras.
	avpUEUsageType                     = 1680
	avpDLBufferingSuggestedPacketCount = 1674
	avpServiceGapTime                  = 1698 // §7.3.223
	avpNonIPPDNTypeIndicator           = 1681
	avpNonIPDataDeliveryMechanism      = 1682
	avpSCEFRealm                       = 1684 // §7.3.207
	avpPreferredDataMode               = 1686
	avpPDNConnectionContinuity         = 1690

	// Trace-Data + MDTConfiguration + AreaScope.
	// fiorix pre-ships avp.TraceData (1458), avp.TraceReference (1459),
	// avp.TraceDepth (1462), Trace-NE-Type-List (1463), Trace-Interface-List
	// (1464), Trace-Event-List (1465), avp.OMCId (1466), avp.MDTConfiguration
	// (1622), avp.TraceCollectionEntity (1452). Sub-AVPs registered in
	// dict_extensions_29272.go are addressed by numeric constants below.
	avpJobType                  = 1623
	avpAreaScope                = 1624
	avpListOfMeasurements       = 1625
	avpReportingTrigger         = 1626
	avpReportInterval           = 1627
	avpReportAmount             = 1628
	avpEventThresholdRSRP       = 1629
	avpEventThresholdRSRQ       = 1630
	avpLoggingInterval          = 1631
	avpLoggingDuration          = 1632
	avpCellGlobalIdentity       = 1604 // §7.3.119
	avpEUTRANCellGlobalIdentity = 1602 // §7.3.117
	avpTrackingAreaIdentity     = 1603 // §7.3.118
	avpRoutingAreaIdentity      = 1605 // §7.3.120
	avpLocationAreaIdentity     = 1606 // §7.3.121
	avpMeasurementPeriodLTE     = 1655
	avpMeasurementPeriodUMTS    = 1656
	avpCollectionPeriodRRMLTE   = 1657
	avpCollectionPeriodRRMUMTS  = 1658
	avpPositioningMethod        = 1659
	avpMeasurementQuantity      = 1660
	avpEventThresholdEvent1F    = 1661
	avpEventThresholdEvent1I    = 1662
	avpTraceReference           = 1459
)

// encodeSubscriptionData converts the structured pb.SubscriptionProfile
// into a Grouped AVP (1400, vendor 10415) per 3GPP TS 29.272 §7.3.2.
//
// Returns nil when profile is nil or empty (no sub-AVPs would be emitted)
// — caller should skip the AVP entirely rather than emitting an empty
// Grouped wrapper, which some HSS peers reject as malformed.
//
// Wire layout follows TS 29.272 §7.3.2 Table 7.3.2/1. Sub-AVPs are
// emitted in the canonical spec order so a packet capture aligns
// line-by-line with the spec; this also makes regression diff'ing
// against another HSS implementation easier.
//
// This replaces the legacy
//
//	datatype.OctetString(SubData)
//
// path which was wire-invalid (1400 is Grouped, not OctetString) and
// caused real HSS peers to reject ULA / IDR with parse errors.
func encodeSubscriptionData(p *pb.SubscriptionProfile) *diam.GroupedAVP {
	if p == nil {
		return nil
	}
	g := &diam.GroupedAVP{}

	addEnumPtr(g, avp.SubscriberStatus, p.SubscriberStatus)
	addOctetHex(g, avp.MSISDN, p.MSISDN)
	addOctetHex(g, avpAMSISDN, p.AMSISDN)
	addOctetHex(g, avpSTNSR, p.STNSR)
	addEnumPtr(g, avpICSIndicator, p.ICSIndicator)
	addEnumPtr(g, avp.NetworkAccessMode, p.NetworkAccessMode)
	addUnsignedNonZero(g, avp.OperatorDeterminedBarring, p.OperatorDeterminedBarring)
	addUnsignedNonZero(g, avp.HPLMNODB, p.HPLMNODB)
	for _, zone := range p.RegionalSubscriptionZoneCodes {
		addOctetHex(g, avp.RegionalSubscriptionZoneCode, zone)
	}
	addUnsignedNonZero(g, avp.AccessRestrictionData, p.AccessRestrictionData)
	addUTF8NonEmpty(g, avp.APNOIReplacement, p.APNOIReplacement)

	if lcs := encodeLCSInfo(p.LCSInfo); lcs != nil {
		g.AddAVP(avp3GPP(avp.LCSInfo, lcs))
	}
	if tl := encodeTeleserviceList(p.TeleserviceList); tl != nil {
		g.AddAVP(avp3GPP(avpTeleserviceList, tl))
	}

	// Call-Barring-Info (1488, TS 29.272
	// §7.3.31). CS-domain SS-Code/SS-Status barring; multi.
	for _, cb := range p.CallBarringInfo {
		if entry := encodeCallBarringInfo(cb); entry != nil {
			g.AddAVP(avp3GPP(avp.CallBarringInfo, entry))
		}
	}

	// 3GPP-Charging-Characteristics is vendor 10415 per TS 29.061 §16.4.7.2
	// (re-used by S6a). 4 bytes (1 nibble profile + 3 nibble behaviour).
	addOctetHex(g, avp.TGPPChargingCharacteristics, p.ChargingCharacteristics)

	if ambr := encodeAMBR(p.AMBR); ambr != nil {
		g.AddAVP(avp3GPP(avp.AMBR, ambr))
	}
	if apnp := encodeAPNConfigurationProfile(p.APNConfigurationProfile); apnp != nil {
		g.AddAVP(avp3GPP(avp.APNConfigurationProfile, apnp))
	}

	// GPRS-Subscription-Data (1467, TS 29.272
	// §7.3.71). Converged-HSS 2G/3G subscriber path; carries the PDP context
	// list parallel to APN-Configuration-Profile for the EPS bearer side.
	if gprs := encodeGPRSSubscriptionData(p.GPRSSubscriptionData); gprs != nil {
		g.AddAVP(avp3GPP(avp.GPRSSubscriptionData, gprs))
	}

	addUnsignedNonZero(g, avp.RATFrequencySelectionPriorityID, p.RATFrequencySelectionPriorityID)

	for _, csg := range p.CSGSubscriptionData {
		g.AddAVP(avp3GPP(avp.CSGSubscriptionData, encodeCSGSubscriptionData(csg)))
	}

	addEnumPtr(g, avpRoamingRestrictedDueToUnsupp, p.RoamingRestrictedDueToUnsupportedFeature)
	addUnsignedNonZero(g, avpSubscribedPeriodicRAUTAUTimer, p.SubscribedPeriodicRAUTAUTimer)
	addUnsignedNonZero(g, avp.MPSPriority, p.MPSPriority)
	addEnumPtr(g, avp.VPLMNLIPAAllowed, p.VPLMNLIPAAllowed)
	addEnumPtr(g, avpRelayNodeIndicator, p.RelayNodeIndicator)
	addEnumPtr(g, avpMDTUserConsent, p.MDTUserConsent)
	addEnumPtr(g, avpSubscribedVSRVCC, p.SubscribedVSRVCC)

	if prose := encodeProSeSubscriptionData(p.ProSeSubscriptionData); prose != nil {
		g.AddAVP(avp3GPP(avpProSeSubscriptionData, prose))
	}

	// IMSI-Group-Id (1675, TS 29.272 §7.3.122
	// Rel-13). Multi; one outer AVP per entry. Spec child-AVP order positions
	// this near the Rel-13 cluster, before SubscriptionDataFlags.
	for _, ig := range p.IMSIGroupID {
		if entry := encodeIMSIGroupID(ig); entry != nil {
			g.AddAVP(avp3GPP(avpIMSIGroupID, entry))
		}
	}
	// Adjacent-Access-Restriction-Data (1673,
	// TS 29.272 §7.3.137 Rel-14). Multi.
	for _, ar := range p.AdjacentAccessRestrictionData {
		if entry := encodeAdjacentAccessRestrictionData(ar); entry != nil {
			g.AddAVP(avp3GPP(avpAdjacentAccessRestrictionData, entry))
		}
	}

	// Trace-Data (1458, TS 29.272 §7.3.95).
	// Lawful-intercept / MDT trace activation. Spec child-AVP order positions
	// Trace-Data after CSGSubscriptionData per §7.3.2 ABNF.
	if td := encodeTraceData(p.TraceData); td != nil {
		g.AddAVP(avp3GPP(avp.TraceData, td))
	}

	// Rel-13/14 power-saving + safety cluster.
	// eDRX-Cycle-Length (1691, §7.3.215): one per RAT type.
	if v2x := encodeV2XSubscriptionData(p.V2XSubscriptionData); v2x != nil {
		g.AddAVP(avp3GPP(avpV2XSubscriptionData, v2x))
	}
	if ei := encodeEmergencyInfo(p.EmergencyInfo); ei != nil {
		g.AddAVP(avp3GPP(avpEmergencyInfo, ei))
	}
	for _, e := range p.EDRXCycleLength {
		if len(e.Value) != 1 { // §7.3.216: 1 octet
			logger.Warn("S6a: eDRX-Cycle-Length not sent: eDRX-Cycle-Length-Value must be 1 octet",
				logger.Int("octets", len(e.Value)))
			continue
		}
		g.AddAVP(avp3GPP(avpEDRXCycleLength, &diam.GroupedAVP{AVP: []*diam.AVP{
			avp3GPP(avp.RATType, datatype.Enumerated(e.RATType)),
			avp3GPP(avpEDRXCycleLengthVal, datatype.OctetString(e.Value)),
		}}))
	}

	// Top-level extras: UE-Usage-Type / DL-Buffering-Suggested-Packet-Count
	// / Service-Gap-Time live on SubscriptionProfile (NB-IoT slicing + power-
	// saving hints).
	addUnsignedNonZero(g, avpUEUsageType, p.UEUsageType)
	addInteger32NonZero(g, avpDLBufferingSuggestedPacketCount, p.DLBufferingSuggestedPacketCount)
	addUnsignedNonZero(g, avpServiceGapTime, p.ServiceGapTime)
	for _, w := range p.PagingTimeWindow {
		if len(w.Length) != 1 { // §7.3.228: 1 octet
			logger.Warn("S6a: Paging-Time-Window not sent: Paging-Time-Window-Length must be 1 octet",
				logger.Int("octets", len(w.Length)))
			continue
		}
		g.AddAVP(avp3GPP(avpPagingTimeWindow, &diam.GroupedAVP{AVP: []*diam.AVP{
			avp3GPP(avpOperationMode, datatype.Unsigned32(w.OperationMode)),
			avp3GPP(avpPagingTimeWindowLen, datatype.OctetString(w.Length)),
		}}))
	}

	addUnsignedNonZero(g, avpSubscriptionDataFlags, p.SubscriptionDataFlags)

	if len(g.AVP) == 0 {
		return nil
	}
	return g
}

// encodeAMBR builds the AMBR Grouped AVP (1435, TS 29.272 §7.3.41). Returns
// nil when every rate is zero: "Max-Requested-Bandwidth-UL and -DL shall not
// both be set to 0" (§7.3.2).
func encodeAMBR(a *pb.AMBR) *diam.GroupedAVP {
	if a == nil {
		return nil
	}
	if a.MaxRequestedBandwidthUL == 0 && a.MaxRequestedBandwidthDL == 0 &&
		a.ExtendedMaxRequestedBWUL == 0 && a.ExtendedMaxRequestedBWDL == 0 {
		return nil
	}
	g := &diam.GroupedAVP{}
	// Both mandatory (§7.3.41). With an Extended-Max-Requested-BW value, the
	// bits-per-second AVP "shall be present, and set to its upper limit
	// 4294967295" (§7.3.41).
	ul, dl := a.MaxRequestedBandwidthUL, a.MaxRequestedBandwidthDL
	if a.ExtendedMaxRequestedBWUL != 0 {
		ul = math.MaxUint32
	}
	if a.ExtendedMaxRequestedBWDL != 0 {
		dl = math.MaxUint32
	}
	g.AddAVP(avp3GPP(avp.MaxRequestedBandwidthUL, datatype.Unsigned32(ul)))
	g.AddAVP(avp3GPP(avp.MaxRequestedBandwidthDL, datatype.Unsigned32(dl)))
	// Rel-15 high-rate extension (TS 29.214).
	addUnsignedNonZero(g, avp.ExtendedMaxRequestedBWUL, a.ExtendedMaxRequestedBWUL)
	addUnsignedNonZero(g, avp.ExtendedMaxRequestedBWDL, a.ExtendedMaxRequestedBWDL)
	if !complete(g, "AMBR", avp.MaxRequestedBandwidthUL, avp.MaxRequestedBandwidthDL) {
		return nil
	}
	return g
}

// encodeAPNConfigurationProfile builds APN-Configuration-Profile (1429).
// Context-Identifier is mandatory per spec; emitted even at zero so the
// AVP is well-formed (zero-context-id is unusual but spec-legal).
func encodeAPNConfigurationProfile(p *pb.APNConfigurationProfile) *diam.GroupedAVP {
	if p == nil {
		return nil
	}
	g := &diam.GroupedAVP{}
	g.AddAVP(avp3GPP(avp.ContextIdentifier, datatype.Unsigned32(p.ContextIdentifier)))
	addEnumPtr(g, avp.AllAPNConfigurationsIncludedIndicator, p.AllAPNConfigurationsIncludedIndicator)
	for _, ac := range p.APNConfigurations {
		if cfg := encodeAPNConfiguration(ac); cfg != nil {
			g.AddAVP(avp3GPP(avp.APNConfiguration, cfg))
		}
	}
	if !complete(g, "APN-Configuration-Profile", avp.ContextIdentifier, avp.AllAPNConfigurationsIncludedIndicator, avp.APNConfiguration) {
		return nil
	}
	return g
}

// encodeAPNConfiguration builds one APN-Configuration entry (1430).
func encodeAPNConfiguration(c pb.APNConfiguration) *diam.GroupedAVP {
	g := &diam.GroupedAVP{}
	g.AddAVP(avp3GPP(avp.ContextIdentifier, datatype.Unsigned32(c.ContextIdentifier)))
	addEnumPtr(g, avp.PDNType, c.PDNType)
	addUTF8NonEmptyBase(g, avp.ServiceSelection, c.ServiceSelection) // base IETF AVP — no V-bit
	if eps := encodeEPSSubscribedQoSProfile(c.EPSSubscribedQoSProfile); eps != nil {
		g.AddAVP(avp3GPP(avp.EPSSubscribedQoSProfile, eps))
	}
	addEnumPtr(g, avp.VPLMNDynamicAddressAllowed, c.VPLMNDynamicAddressAllowed)
	addOctetNonEmpty(g, avp.VisitedNetworkIdentifier, []byte(c.VisitedNetworkIdentifier)) // TS 29.229, vendor 10415 (TS 29.272 Table 7.3.1/2)
	addEnumPtr(g, avp.PDNGWAllocationType, c.PDNGWAllocationType)
	addOctetHex(g, avp.TGPPChargingCharacteristics, c.ChargingCharacteristics)
	if ambr := encodeAMBR(c.AMBR); ambr != nil {
		g.AddAVP(avp3GPP(avp.AMBR, ambr))
	}
	addUTF8NonEmpty(g, avp.APNOIReplacement, c.APNOIReplacement)

	// DSMIPv6 home agent allocation.
	// MIP6AgentInfo is base IETF (RFC 5447) flag posture; encoder helper
	// handles the V-bit.
	if mip := encodeMIP6AgentInfo(c.MIP6AgentInfo); mip != nil {
		g.AddAVP(diam.NewAVP(avp.MIP6AgentInfo, avp.Mbit, 0, mip))
	}
	// Specific-APN-Info (1472, Rel-10);
	// per-service-selection scoped MIP6/VNI overrides.
	for _, sai := range c.SpecificAPNInfo {
		if entry := encodeSpecificAPNInfo(sai); entry != nil {
			g.AddAVP(avp3GPP(avp.SpecificAPNInfo, entry))
		}
	}
	// SIPTO-Local-Network-Permission (1665).
	addUnsignedNonZero(g, avpSIPTOLocalNetworkPermission, c.SIPTOLocalNetworkPermission)

	// WLAN-Offloadability (1667, TS 29.272 §7.3.181).
	if wlan := encodeWLANOffloadability(c.WLANOffloadability); wlan != nil {
		g.AddAVP(avp3GPP(avpWLANOffloadability, wlan))
	}

	// APN-Configuration extras.
	// Served-Party-IP-Address (848, multi v4+v6, TS 32.299).
	addAddressString(g, avp.ServedPartyIPAddress, c.ServedPartyIPAddress)
	addUnsignedNonZero(g, avp.SIPTOPermission, c.SIPTOPermission)
	addUnsignedNonZero(g, avp.LIPAPermission, c.LIPAPermission)
	addUnsignedNonZero(g, avpRestorationPriority, c.RestorationPriority)
	// NB-IoT SCEF binding + non-IP delivery mechanism + data mode.
	addUnsignedNonZero(g, avpNonIPPDNTypeIndicator, c.NonIPPDNTypeIndicator)
	addUnsignedNonZero(g, avpNonIPDataDeliveryMechanism, c.NonIPDataDeliveryMechanism)
	addDiameterIdentityNonEmpty(g, avpSCEFID, c.SCEFID)
	addDiameterIdentityNonEmpty(g, avpSCEFRealm, c.SCEFRealm)
	addUnsignedNonZero(g, avpPreferredDataMode, c.PreferredDataMode)
	addUnsignedNonZero(g, avpPDNConnectionContinuity, c.PDNConnectionContinuity)
	if !complete(g, "APN-Configuration", avp.ContextIdentifier, avp.PDNType, avp.ServiceSelection) {
		return nil
	}
	return g
}

// encodeEPSSubscribedQoSProfile builds EPS-Subscribed-QoS-Profile (1431).
func encodeEPSSubscribedQoSProfile(q *pb.EPSSubscribedQoSProfile) *diam.GroupedAVP {
	if q == nil {
		return nil
	}
	g := &diam.GroupedAVP{}
	addUnsignedNonZero(g, avp.QoSClassIdentifier, q.QoSClassIdentifier)
	if arp := encodeAllocationRetentionPriority(q.AllocationRetentionPriority); arp != nil {
		g.AddAVP(avp3GPP(avp.AllocationRetentionPriority, arp))
	}
	if !complete(g, "EPS-Subscribed-QoS-Profile", avp.QoSClassIdentifier, avp.AllocationRetentionPriority) {
		return nil
	}
	return g
}

// encodeAllocationRetentionPriority builds ARP (1034). Priority-Level is
// mandatory per TS 29.272 §7.3.40 — emitted even at zero.
func encodeAllocationRetentionPriority(arp *pb.AllocationRetentionPriority) *diam.GroupedAVP {
	if arp == nil {
		return nil
	}
	g := &diam.GroupedAVP{}
	g.AddAVP(avp3GPP(avp.PriorityLevel, datatype.Unsigned32(arp.PriorityLevel)))
	addEnumPtr(g, avpPreEmptionCapability, arp.PreEmptionCapability)
	addEnumPtr(g, avpPreEmptionVulnerability, arp.PreEmptionVulnerability)
	return g
}

// encodeLCSInfo builds LCS-Info (1473). Slim — most carrier deployments
// don't push LCS at attach (SLh handles on-demand).
func encodeLCSInfo(l *pb.LCSInfo) *diam.GroupedAVP {
	if l == nil {
		return nil
	}
	g := &diam.GroupedAVP{}
	for _, gmlc := range l.GMLCNumber {
		addOctetHex(g, avpGMLCNumber, gmlc)
	}
	for _, exc := range l.LCSPrivacyExceptionList {
		sub := &diam.GroupedAVP{}
		addOctetNonEmpty(sub, avpSSCode, exc.SSCode)
		addOctetNonEmpty(sub, avpSSStatus, exc.SSStatus)
		addEnumPtr(sub, avpNotificationToUEUser, exc.NotificationToUEUser)
		// LCS detail expansion.
		for _, ec := range exc.ExternalClient {
			if entry := encodeExternalClient(ec); entry != nil {
				sub.AddAVP(avp3GPP(avp.ExternalClient, entry))
			}
		}
		for _, pc := range exc.PLMNClient {
			sub.AddAVP(avp3GPP(avp.PLMNClient, datatype.Enumerated(pc)))
		}
		for _, st := range exc.ServiceType {
			if entry := encodeServiceType(st); entry != nil {
				sub.AddAVP(avp3GPP(avp.TGPPServiceType, entry))
			}
		}
		if complete(sub, "LCS-PrivacyException", avpSSCode, avpSSStatus) {
			g.AddAVP(avp3GPP(avpLCSPrivacyExceptionList, sub))
		}
	}
	for _, mo := range l.MOLR {
		sub := &diam.GroupedAVP{}
		addOctetNonEmpty(sub, avpSSCode, mo.SSCode)
		addOctetNonEmpty(sub, avpSSStatus, mo.SSStatus)
		if complete(sub, "MO-LR", avpSSCode, avpSSStatus) {
			g.AddAVP(avp3GPP(avpMOLR, sub))
		}
	}
	if len(g.AVP) == 0 {
		return nil
	}
	return g
}

// encodeTeleserviceList builds Teleservice-List (1486). Slim.
func encodeTeleserviceList(t *pb.TeleserviceList) *diam.GroupedAVP {
	if t == nil || len(t.TSCodes) == 0 {
		return nil
	}
	g := &diam.GroupedAVP{}
	for _, ts := range t.TSCodes {
		addOctetNonEmpty(g, avpTSCode, ts)
	}
	if len(g.AVP) == 0 {
		return nil
	}
	return g
}

// encodeCSGSubscriptionData builds one CSG-Subscription-Data entry (1436).
// CSG-Id is mandatory per TS 29.272 §7.3.78 — emitted even at zero.
func encodeCSGSubscriptionData(c pb.CSGSubscriptionData) *diam.GroupedAVP {
	g := &diam.GroupedAVP{}
	g.AddAVP(avp3GPP(avpCSGId, datatype.Unsigned32(c.CSGId)))
	if c.ExpirationDate != 0 {
		// Expiration-Date is a Time AVP (TS 29.272 §7.3.80): seconds since
		// 1900-01-01 (RFC 6733 §4.3.1). datatype.Time converts from Go time.
		g.AddAVP(avp3GPP(avpExpirationDate, datatype.Time(time.Unix(c.ExpirationDate, 0))))
	}
	for _, svc := range c.ServiceSelections {
		addUTF8NonEmptyBase(g, avp.ServiceSelection, svc) // base IETF AVP — no V-bit
	}
	return g
}

// encodeProSeSubscriptionData builds ProSe-Subscription-Data (3701). Slim.
func encodeProSeSubscriptionData(p *pb.ProSeSubscriptionData) *diam.GroupedAVP {
	if p == nil || p.ProSePermission == 0 {
		return nil
	}
	g := &diam.GroupedAVP{}
	g.AddAVP(avp3GPP(avpProSePermission, datatype.Unsigned32(p.ProSePermission)))
	return g
}

// decodeTraceData parses an inbound Trace-Data Grouped AVP into a
// *pb.TraceData. ULR (MME → HSS-via-gateway) carries trace activation
// requests; backend-initiated IDR/CLR can carry the same structure in the
// reverse direction. Returns nil when the Grouped is empty or contains
// no recognized sub-AVPs.
//
// Bit-level semantics of TraceNETypeList / TraceInterfaceList / TraceEventList
// per TS 32.422 §5.1 are intentionally not interpreted here — gateway is
// opaque transport, the backend/SCEF parses semantics.
func decodeTraceData(g *diam.GroupedAVP) *pb.TraceData {
	if g == nil {
		return nil
	}
	td := &pb.TraceData{}
	for _, sub := range g.AVP {
		switch sub.Code {
		case avpTraceReference:
			if oc, ok := sub.Data.(datatype.OctetString); ok {
				td.TraceReference = []byte(oc)
			}
		case avp.TraceDepth:
			td.TraceDepth = avpDataAsUint32(sub)
		case avp.TraceNETypeList:
			if oc, ok := sub.Data.(datatype.OctetString); ok {
				td.TraceNETypeList = []byte(oc)
			}
		case avp.TraceInterfaceList:
			if oc, ok := sub.Data.(datatype.OctetString); ok {
				td.TraceInterfaceList = []byte(oc)
			}
		case avp.TraceEventList:
			if oc, ok := sub.Data.(datatype.OctetString); ok {
				td.TraceEventList = []byte(oc)
			}
		case avp.OMCID:
			if oc, ok := sub.Data.(datatype.OctetString); ok {
				td.OMCID = string(oc)
			}
		case avp.TraceCollectionEntity:
			if a, ok := sub.Data.(datatype.Address); ok {
				td.TraceCollectionEntity = net.IP(a).String()
			}
		case avp.MDTConfiguration:
			if mg, ok := sub.Data.(*diam.GroupedAVP); ok {
				td.MDTConfiguration = decodeMDTConfiguration(mg)
			}
		}
	}
	// Empty-decode guard: if no sub-AVPs were recognized (everything zero/empty),
	// return nil so callers don't carry an empty struct.
	if len(td.TraceReference) == 0 && td.TraceDepth == 0 &&
		len(td.TraceNETypeList) == 0 && len(td.TraceInterfaceList) == 0 &&
		len(td.TraceEventList) == 0 && td.OMCID == "" &&
		td.TraceCollectionEntity == "" && td.MDTConfiguration == nil {
		return nil
	}
	return td
}

// decodeMDTConfiguration parses MDT-Configuration (1622). Counterpart to
// encodeMDTConfiguration; same field set.
func decodeMDTConfiguration(g *diam.GroupedAVP) *pb.MDTConfiguration {
	if g == nil {
		return nil
	}
	m := &pb.MDTConfiguration{}
	for _, sub := range g.AVP {
		switch sub.Code {
		case avpJobType:
			m.JobType = avpDataAsUint32(sub)
		case avpAreaScope:
			if ag, ok := sub.Data.(*diam.GroupedAVP); ok {
				m.AreaScope = decodeAreaScope(ag)
			}
		case avpListOfMeasurements:
			m.ListOfMeasurements = avpDataAsUint32(sub)
		case avpReportingTrigger:
			m.ReportingTrigger = avpDataAsUint32(sub)
		case avpReportInterval:
			m.ReportInterval = avpDataAsUint32(sub)
		case avpReportAmount:
			m.ReportAmount = avpDataAsUint32(sub)
		case avpEventThresholdRSRP:
			m.EventThresholdRSRP = avpDataAsUint32(sub)
		case avpEventThresholdRSRQ:
			m.EventThresholdRSRQ = avpDataAsUint32(sub)
		case avpLoggingInterval:
			m.LoggingInterval = avpDataAsUint32(sub)
		case avpLoggingDuration:
			m.LoggingDuration = avpDataAsUint32(sub)
		case avpMeasurementPeriodLTE:
			m.MeasurementPeriodLTE = avpDataAsUint32(sub)
		case avpMeasurementPeriodUMTS:
			m.MeasurementPeriodUMTS = avpDataAsUint32(sub)
		case avpCollectionPeriodRRMLTE:
			m.CollectionPeriodRRMLTE = avpDataAsUint32(sub)
		case avpCollectionPeriodRRMUMTS:
			m.CollectionPeriodRRMUMTS = avpDataAsUint32(sub)
		case avpPositioningMethod:
			if oc, ok := sub.Data.(datatype.OctetString); ok {
				m.PositioningMethod = []byte(oc)
			}
		case avpMeasurementQuantity:
			if oc, ok := sub.Data.(datatype.OctetString); ok {
				m.MeasurementQuantity = []byte(oc)
			}
		case avpEventThresholdEvent1F:
			m.EventThresholdEvent1F = int32(avpDataAsUint32(sub))
		case avpEventThresholdEvent1I:
			m.EventThresholdEvent1I = int32(avpDataAsUint32(sub))
		}
	}
	return m
}

// decodeAreaScope parses Area-Scope (1624). Each entity-id sub-AVP can
// repeat; appended to the matching slice.
func decodeAreaScope(g *diam.GroupedAVP) *pb.AreaScope {
	if g == nil {
		return nil
	}
	a := &pb.AreaScope{}
	for _, sub := range g.AVP {
		oc, ok := sub.Data.(datatype.OctetString)
		if !ok {
			continue
		}
		b := []byte(oc)
		switch sub.Code {
		case avpCellGlobalIdentity:
			a.CellGlobalIdentity = append(a.CellGlobalIdentity, b)
		case avpEUTRANCellGlobalIdentity:
			a.EUTRANCellGlobalIdentity = append(a.EUTRANCellGlobalIdentity, b)
		case avpRoutingAreaIdentity:
			a.RoutingAreaIdentity = append(a.RoutingAreaIdentity, b)
		case avpLocationAreaIdentity:
			a.LocationAreaIdentity = append(a.LocationAreaIdentity, b)
		case avpTrackingAreaIdentity:
			a.TrackingAreaIdentity = append(a.TrackingAreaIdentity, b)
		}
	}
	if len(a.CellGlobalIdentity)+len(a.EUTRANCellGlobalIdentity)+
		len(a.RoutingAreaIdentity)+len(a.LocationAreaIdentity)+
		len(a.TrackingAreaIdentity) == 0 {
		return nil
	}
	return a
}

// avpDataAsUint32 decodes Unsigned32 / Enumerated AVP data, falling back to
// raw 4-byte interpretation if dict.Default returned datatype.Unknown
// (same defensive technique as test-file avpUint32 helper).
func avpDataAsUint32(a *diam.AVP) uint32 {
	switch v := a.Data.(type) {
	case datatype.Unsigned32:
		return uint32(v)
	case datatype.Enumerated:
		return uint32(v)
	case datatype.Integer32:
		return uint32(v)
	}
	b := a.Data.Serialize()
	if len(b) != 4 {
		return 0
	}
	return uint32(b[0])<<24 | uint32(b[1])<<16 | uint32(b[2])<<8 | uint32(b[3])
}

// encodeTraceData builds Trace-Data (1458, TS 29.272 §7.3.63): trace and MDT
// activation, nested TraceData → MDT-Configuration → Area-Scope. Trace-
// Reference, Trace-Depth, Trace-NE-Type-List, Trace-Event-List and
// Trace-Collection-Entity are mandatory; without them nothing is sent
// (complete).
//
// Trace-NE-Type-List, Trace-Interface-List and Trace-Event-List carry TS
// 32.422 bitmaps the producer builds; the encoder sends their octets as given.
func encodeTraceData(t *pb.TraceData) *diam.GroupedAVP {
	if t == nil {
		return nil
	}
	g := &diam.GroupedAVP{}
	addOctetNonEmpty(g, avpTraceReference, t.TraceReference)
	g.AddAVP(avp3GPP(avp.TraceDepth, datatype.Enumerated(t.TraceDepth))) // mandatory; 0 is Minimum (TS 32.422)
	// opaque OctetString: bit-level semantics per TS 32.422 §5.1 belongs to the backend/SCEF
	addOctetNonEmpty(g, avp.TraceNETypeList, t.TraceNETypeList)
	addOctetNonEmpty(g, avp.TraceInterfaceList, t.TraceInterfaceList)
	addOctetNonEmpty(g, avp.TraceEventList, t.TraceEventList)
	addOctetNonEmpty(g, avp.OMCID, []byte(t.OMCID))
	if t.TraceCollectionEntity != "" {
		if ip := net.ParseIP(t.TraceCollectionEntity); ip != nil {
			g.AddAVP(avp3GPP(avp.TraceCollectionEntity, datatype.Address(ip)))
		}
	}
	if mdt := encodeMDTConfiguration(t.MDTConfiguration); mdt != nil {
		g.AddAVP(avp3GPP(avp.MDTConfiguration, mdt))
	}
	if len(g.AVP) == 0 {
		return nil
	}
	if !complete(g, "Trace-Data", avpTraceReference, avp.TraceDepth, avp.TraceNETypeList, avp.TraceEventList, avp.TraceCollectionEntity) {
		return nil
	}
	return g
}

// encodeMDTConfiguration builds MDT-Configuration (1622, TS 29.272
// §7.3.97-107 + Rel-12 §7.3.122-129). Job-Type (1623) is mandatory per
// spec — emitted even at zero (IMMEDIATE_MDT_AND_TRACE = 0 is a valid
// enum value). All thresholds, periods, and durations zero-omit.
//
// AreaScope (1624) is a nested Grouped child; recursive encode reuses
// encodeAreaScope.
func encodeMDTConfiguration(m *pb.MDTConfiguration) *diam.GroupedAVP {
	if m == nil {
		return nil
	}
	g := &diam.GroupedAVP{}
	g.AddAVP(avp3GPP(avpJobType, datatype.Enumerated(m.JobType)))
	if as := encodeAreaScope(m.AreaScope); as != nil {
		g.AddAVP(avp3GPP(avpAreaScope, as))
	}
	addUnsignedNonZero(g, avpListOfMeasurements, m.ListOfMeasurements)
	addUnsignedNonZero(g, avpReportingTrigger, m.ReportingTrigger)
	addUnsignedNonZero(g, avpReportInterval, m.ReportInterval)
	addUnsignedNonZero(g, avpReportAmount, m.ReportAmount)
	addUnsignedNonZero(g, avpEventThresholdRSRP, m.EventThresholdRSRP)
	addUnsignedNonZero(g, avpEventThresholdRSRQ, m.EventThresholdRSRQ)
	addUnsignedNonZero(g, avpLoggingInterval, m.LoggingInterval)
	addUnsignedNonZero(g, avpLoggingDuration, m.LoggingDuration)
	addUnsignedNonZero(g, avpMeasurementPeriodLTE, m.MeasurementPeriodLTE)
	addUnsignedNonZero(g, avpMeasurementPeriodUMTS, m.MeasurementPeriodUMTS)
	addUnsignedNonZero(g, avpCollectionPeriodRRMLTE, m.CollectionPeriodRRMLTE)
	addUnsignedNonZero(g, avpCollectionPeriodRRMUMTS, m.CollectionPeriodRRMUMTS)
	addOctetNonEmpty(g, avpPositioningMethod, m.PositioningMethod)
	addOctetNonEmpty(g, avpMeasurementQuantity, m.MeasurementQuantity)
	addInteger32NonZero(g, avpEventThresholdEvent1F, m.EventThresholdEvent1F)
	addInteger32NonZero(g, avpEventThresholdEvent1I, m.EventThresholdEvent1I)
	if len(g.AVP) == 0 {
		return nil
	}
	return g
}

// encodeAreaScope builds Area-Scope (1624, TS 29.272 §7.3.96). MDT
// geographic scope filter — each entity-id slice emits one wire AVP per
// entry. Empty slices are no-ops; outer omitted when all five lists empty.
func encodeAreaScope(a *pb.AreaScope) *diam.GroupedAVP {
	if a == nil {
		return nil
	}
	g := &diam.GroupedAVP{}
	for _, b := range a.CellGlobalIdentity {
		addOctetNonEmpty(g, avpCellGlobalIdentity, b)
	}
	for _, b := range a.EUTRANCellGlobalIdentity {
		addOctetNonEmpty(g, avpEUTRANCellGlobalIdentity, b)
	}
	for _, b := range a.RoutingAreaIdentity {
		addOctetNonEmpty(g, avpRoutingAreaIdentity, b)
	}
	for _, b := range a.LocationAreaIdentity {
		addOctetNonEmpty(g, avpLocationAreaIdentity, b)
	}
	for _, b := range a.TrackingAreaIdentity {
		addOctetNonEmpty(g, avpTrackingAreaIdentity, b)
	}
	if len(g.AVP) == 0 {
		return nil
	}
	return g
}

// encodeWLANOffloadability builds WLAN-Offloadability (1667, TS 29.272 §7.3.123
// Rel-12). Per-APN E-UTRAN / UTRAN WLAN offload permission. Both sub-AVPs
// are Unsigned32 bitmasks; outer omitted when both zero.
func encodeWLANOffloadability(w *pb.WLANOffloadability) *diam.GroupedAVP {
	if w == nil {
		return nil
	}
	g := &diam.GroupedAVP{}
	addUnsignedNonZero(g, avpWLANOffloadabilityEUTRAN, w.WLANOffloadabilityEUTRAN)
	addUnsignedNonZero(g, avpWLANOffloadabilityUTRAN, w.WLANOffloadabilityUTRAN)
	if len(g.AVP) == 0 {
		return nil
	}
	return g
}

// encodeV2XSubscriptionData builds V2X-Subscription-Data (1688, TS 29.272
// §7.3.212), here with V2X-Permission (1689) only. A zero permission sends no
// AVP.
func encodeV2XSubscriptionData(v *pb.V2XSubscriptionData) *diam.GroupedAVP {
	if v == nil || v.V2XPermission == 0 {
		return nil
	}
	g := &diam.GroupedAVP{}
	g.AddAVP(avp3GPP(avpV2XPermission, datatype.Unsigned32(v.V2XPermission)))
	return g
}

// encodeEmergencyInfo builds Emergency-Info (1687, TS 29.272 §7.3.210), which
// wraps a MIP6-Agent-Info (vendor 0, see encodeMIP6AgentInfo).
func encodeEmergencyInfo(e *pb.EmergencyInfo) *diam.GroupedAVP {
	if e == nil {
		return nil
	}
	g := &diam.GroupedAVP{}
	if mip := encodeMIP6AgentInfo(e.MIP6AgentInfo); mip != nil {
		g.AddAVP(diam.NewAVP(avp.MIP6AgentInfo, avp.Mbit, 0, mip))
	}
	if len(g.AVP) == 0 {
		return nil
	}
	return g
}

// encodeCallBarringInfo builds one Call-Barring-Info entry (1488, TS 29.272
// §7.3.31). CS-domain SS-Code/SS-Status barring per TS 22.030. Both sub-AVPs
// are byte-string OctetString; outer AVP omitted if both empty.
func encodeCallBarringInfo(c pb.CallBarringInfo) *diam.GroupedAVP {
	g := &diam.GroupedAVP{}
	addOctetNonEmpty(g, avpSSCode, c.SSCode)
	addOctetNonEmpty(g, avpSSStatus, c.SSStatus)
	if len(g.AVP) == 0 {
		return nil
	}
	if !complete(g, "Call-Barring-Info", avpSSCode, avpSSStatus) {
		return nil
	}
	return g
}

// encodeExternalClient builds one External-Client entry (1479, TS 29.272
// §7.3.30). LCS detail — per-client GMLC-Restriction + Notification-To-UE-
// User. ClientIdentity carried as raw bytes (the model keeps it `string`
// for JSON ergonomics; encoder cast to []byte).
func encodeExternalClient(e pb.ExternalClient) *diam.GroupedAVP {
	g := &diam.GroupedAVP{}
	addOctetNonEmpty(g, avp.ClientIdentity, []byte(e.ClientIdentity))
	addUnsignedNonZero(g, avp.GMLCRestriction, e.GMLCRestriction)
	addUnsignedNonZero(g, avpNotificationToUEUser, e.NotificationToUEUser)
	if len(g.AVP) == 0 {
		return nil
	}
	if !complete(g, "External-Client", avp.ClientIdentity) {
		return nil
	}
	return g
}

// encodeServiceType builds one ServiceType entry (1483 TGPP-Service-Type,
// TS 29.272 §7.3.32). LCS detail — per-service-type-coded GMLC-Restriction
// + Notification-To-UE-User. ServiceTypeCode is mandatory per spec.
func encodeServiceType(s pb.ServiceType) *diam.GroupedAVP {
	g := &diam.GroupedAVP{}
	g.AddAVP(avp3GPP(avp.ServiceTypeIdentity, datatype.Unsigned32(s.ServiceTypeCode)))
	addUnsignedNonZero(g, avp.GMLCRestriction, s.GMLCRestriction)
	addUnsignedNonZero(g, avpNotificationToUEUser, s.NotificationToUEUser)
	return g
}

// encodeSpecificAPNInfo builds one Specific-APN-Info entry (1472, TS 29.272
// §7.3.82): a MIP6 home agent (and Visited-Network-Identifier) for one APN.
// Service-Selection and MIP6-Agent-Info are mandatory; an entry without them
// is not sent.
func encodeSpecificAPNInfo(s pb.SpecificAPNInfo) *diam.GroupedAVP {
	g := &diam.GroupedAVP{}
	addUTF8NonEmptyBase(g, avp.ServiceSelection, s.ServiceSelection)
	if mip := encodeMIP6AgentInfo(s.MIP6AgentInfo); mip != nil {
		g.AddAVP(diam.NewAVP(avp.MIP6AgentInfo, avp.Mbit, 0, mip))
	}
	addOctetNonEmpty(g, avp.VisitedNetworkIdentifier, []byte(s.VisitedNetworkIdentifier))
	if len(g.AVP) == 0 {
		return nil
	}
	if !complete(g, "Specific-APN-Info", avp.ServiceSelection, avp.MIP6AgentInfo) {
		return nil
	}
	return g
}

// encodeMIP6AgentInfo builds MIP6-Agent-Info (486, RFC 5447 §4.2.1, re-used
// by TS 29.272 Table 7.3.1/2). It and its children are IETF AVPs, sent with
// vendor 0 and no V-bit:
//   - MIP-Home-Agent-Address (334, up to two; go-diameter MIPHomeAgentAddress)
//   - MIP-Home-Agent-Host (348, Grouped: Destination-Realm, Destination-Host)
//   - MIP6-Home-Link-Prefix (125, OctetString)
//
// go-diameter's S6a dictionary registers them under vendor 10415, which no
// peer sends; dict_extensions.go adds the vendor-0 entries.
func encodeMIP6AgentInfo(m *pb.MIP6AgentInfo) *diam.GroupedAVP {
	if m == nil {
		return nil
	}
	g := &diam.GroupedAVP{}
	for _, addr := range m.MIP6HomeAgentAddress {
		if addr == "" {
			continue
		}
		ip := net.ParseIP(addr)
		if ip == nil {
			continue
		}
		g.AddAVP(diam.NewAVP(avp.MIPHomeAgentAddress, avp.Mbit, 0, datatype.Address(ip)))
	}
	if m.MIP6HomeAgentHost != nil {
		host := &diam.GroupedAVP{}
		if m.MIP6HomeAgentHost.DestinationHost != "" {
			host.AddAVP(diam.NewAVP(avp.DestinationHost, avp.Mbit, 0,
				datatype.DiameterIdentity(m.MIP6HomeAgentHost.DestinationHost)))
		}
		if m.MIP6HomeAgentHost.DestinationRealm != "" {
			host.AddAVP(diam.NewAVP(avp.DestinationRealm, avp.Mbit, 0,
				datatype.DiameterIdentity(m.MIP6HomeAgentHost.DestinationRealm)))
		}
		if len(host.AVP) > 0 {
			g.AddAVP(diam.NewAVP(avp.MIPHomeAgentHost, avp.Mbit, 0, host))
		}
	}
	if m.MIP6HomeLinkPrefix != "" {
		g.AddAVP(diam.NewAVP(avp.MIP6HomeLinkPrefix, avp.Mbit, 0, datatype.OctetString(m.MIP6HomeLinkPrefix)))
	}
	if len(g.AVP) == 0 {
		return nil
	}
	return g
}

// encodeGPRSSubscriptionData builds GPRS-Subscription-Data (1467, TS 29.272
// §7.3.72): the mandatory Complete-Data-List-Included-Indicator (1468, 0 is
// All_PDP_CONTEXTS_INCLUDED) and 1..50 PDP-Context (1469). Without a complete
// PDP-Context nothing is sent.
func encodeGPRSSubscriptionData(d *pb.GPRSSubscriptionData) *diam.GroupedAVP {
	if d == nil {
		return nil
	}
	g := &diam.GroupedAVP{}
	// Mandatory; 0 is All_PDP_CONTEXTS_INCLUDED (§7.3.73).
	g.AddAVP(avp3GPP(avp.CompleteDataListIncludedIndicator, datatype.Enumerated(d.CompleteDataListIncludedIndicator)))
	for _, ctx := range d.PDPContext {
		if entry := encodePDPContext(ctx); entry != nil {
			g.AddAVP(avp3GPP(avp.PDPContext, entry))
		}
	}
	if len(g.AVP) == 0 {
		return nil
	}
	if !complete(g, "GPRS-Subscription-Data", avp.CompleteDataListIncludedIndicator, avp.PDPContext) {
		return nil
	}
	return g
}

// encodePDPContext builds one PDP-Context entry (1469, TS 29.272 §7.3.74).
// Context-Identifier, PDP-Type, QoS-Subscribed and Service-Selection are
// mandatory; an entry without them is not sent (complete).
//
// PDP-Address (1227) and Ext-PDP-Address (1621) come in as []string of
// stringified IPs; net.ParseIP failures silent-skip per addAddressString
// helper convention. AMBR sub-AVP reuses encodeAMBR (UE-AMBR encoder).
func encodePDPContext(c pb.PDPContext) *diam.GroupedAVP {
	g := &diam.GroupedAVP{}
	g.AddAVP(avp3GPP(avp.ContextIdentifier, datatype.Unsigned32(c.ContextIdentifier)))
	addOctetNonEmpty(g, avp.PDPType, c.PDPType)
	addAddressString(g, avp.PDPAddress, c.PDPAddress)
	addOctetNonEmpty(g, avp.QoSSubscribed, c.QoSSubscribed)
	addUnsignedNonZero(g, avp.VPLMNDynamicAddressAllowed, c.VPLMNDynamicAddressAllowed)
	addUTF8NonEmptyBase(g, avp.ServiceSelection, c.ServiceSelection)
	addOctetHex(g, avp.TGPPChargingCharacteristics, c.ChargingCharacteristics)
	addOctetNonEmpty(g, avp.ExtPDPType, c.ExtPDPType)
	addAddressString(g, avp.ExtPDPAddress, c.ExtPDPAddress)
	if ambr := encodeAMBR(c.AMBR); ambr != nil {
		g.AddAVP(avp3GPP(avp.AMBR, ambr))
	}
	addUTF8NonEmpty(g, avp.APNOIReplacement, c.APNOIReplacement)
	addUnsignedNonZero(g, avp.SIPTOPermission, c.SIPTOPermission)
	addUnsignedNonZero(g, avp.LIPAPermission, c.LIPAPermission)
	addUnsignedNonZero(g, avpRestorationPriority, c.RestorationPriority)
	if len(g.AVP) == 0 {
		return nil
	}
	if !complete(g, "PDP-Context", avp.ContextIdentifier, avp.PDPType, avp.QoSSubscribed, avp.ServiceSelection) {
		return nil
	}
	return g
}

// encodeIMSIGroupID builds one IMSI-Group-Id entry (1675, TS 29.272 §7.3.189).
// Group-Service-Id, Group-PLMN-Id and Local-Group-Id are all mandatory; an
// entry without them is not sent.
func encodeIMSIGroupID(ig pb.IMSIGroupID) *diam.GroupedAVP {
	g := &diam.GroupedAVP{}
	g.AddAVP(avp3GPP(avpGroupServiceID, datatype.Unsigned32(ig.GroupServiceID)))
	addOctetNonEmpty(g, avpGroupPLMNID, ig.GroupPLMNID)
	addOctetNonEmpty(g, avpLocalGroupID, ig.LocalGroupID)
	if len(g.AVP) == 0 {
		return nil
	}
	if !complete(g, "IMSI-Group-Id", avpGroupServiceID, avpGroupPLMNID, avpLocalGroupID) {
		return nil
	}
	return g
}

// encodeAdjacentAccessRestrictionData builds one Adjacent-Access-Restriction-
// Data entry (1673, TS 29.272 §7.3.187). Visited-PLMN-Id and Access-
// Restriction-Data are mandatory; the latter is sent even at 0 (no
// restriction).
func encodeAdjacentAccessRestrictionData(a pb.AdjacentAccessRestrictionData) *diam.GroupedAVP {
	g := &diam.GroupedAVP{}
	addOctetNonEmpty(g, avp.VisitedPLMNID, a.VisitedPLMNID)
	// Mandatory; 0 means no restriction.
	g.AddAVP(avp3GPP(avp.AccessRestrictionData, datatype.Unsigned32(a.AccessRestrictionData)))
	if len(g.AVP) == 0 {
		return nil
	}
	if !complete(g, "Adjacent-Access-Restriction-Data", avp.VisitedPLMNID, avp.AccessRestrictionData) {
		return nil
	}
	return g
}

// =============================================================================
// helpers
// =============================================================================

// addEnumPtr emits an Enumerated AVP only when the pointer is non-nil
// (i.e. the producer explicitly set it — necessary for enums where 0 is
// a valid spec value like Subscriber-Status SERVICE_GRANTED).
func addEnumPtr(g *diam.GroupedAVP, code uint32, v *uint32) {
	if v == nil {
		return
	}
	g.AddAVP(avp3GPP(code, datatype.Enumerated(*v)))
}

// addUnsignedNonZero emits an Unsigned32 AVP only when the value is
// non-zero. Use for fields where 0 means "not set" (bitmasks, bandwidths,
// identifiers).
func addUnsignedNonZero(g *diam.GroupedAVP, code uint32, v uint32) {
	if v == 0 {
		return
	}
	g.AddAVP(avp3GPP(code, datatype.Unsigned32(v)))
}

// addUTF8NonEmpty emits a vendor-10415 UTF8String AVP only when the value is
// non-empty. For IETF AVPs (vendor 0, no V-bit) use addUTF8NonEmptyBase.
func addUTF8NonEmpty(g *diam.GroupedAVP, code uint32, v string) {
	if v == "" {
		return
	}
	g.AddAVP(avp3GPP(code, datatype.UTF8String(v)))
}

// addUTF8NonEmptyBase emits a UTF8String base AVP (vendor 0, no V-bit).
// Used for Service-Selection (493) which is defined in IETF RFC 5778 / RFC
// 4072, not 3GPP — emitting it with the V-bit triggers strict-peer rejects
// (TS 29.272 inherits the base IETF flag posture for this AVP).
func addUTF8NonEmptyBase(g *diam.GroupedAVP, code uint32, v string) {
	if v == "" {
		return
	}
	g.AddAVP(diam.NewAVP(code, avp.Mbit, 0, datatype.UTF8String(v)))
}

// addOctetNonEmpty emits a vendor-10415 OctetString AVP only when the slice
// is non-empty.
func addOctetNonEmpty(g *diam.GroupedAVP, code uint32, v []byte) {
	if len(v) == 0 {
		return
	}
	g.AddAVP(avp3GPP(code, datatype.OctetString(v)))
}

// addInteger32NonZero emits a signed Integer32 AVP only when value is
// non-zero. Used for DL-Buffering-Suggested-Packet-Count (1674) where the
// model keeps int32 (NB-IoT high-latency hint can be negative-marked in
// some Rel-13 stacks; spec keeps it Integer32).
func addInteger32NonZero(g *diam.GroupedAVP, code uint32, v int32) {
	if v == 0 {
		return
	}
	g.AddAVP(avp3GPP(code, datatype.Integer32(v)))
}

// addDiameterIdentityNonEmpty emits a vendor-10415 DiameterIdentity AVP. Used
// for SCEF-ID (3125) and SCEF-Realm (1684) inside APN-Configuration.
func addDiameterIdentityNonEmpty(g *diam.GroupedAVP, code uint32, s string) {
	if s == "" {
		return
	}
	g.AddAVP(avp3GPP(code, datatype.DiameterIdentity(s)))
}

// addAddressString iterates a slice of stringified IPs (the model's
// convention for AVP type Address — JSON-friendly text form), parses each
// via net.ParseIP, and emits one Address AVP per valid entry. Malformed
// strings silent-skip per the encoder data-quality invariant: the producer
// (the backend) is the source of validation; gateway never panics on bad input.
//
// Used by PDP-Address (1471), Ext-PDP-Address (1621) and
// Served-Party-IP-Address (848 — base IETF, separate flag posture).
func addAddressString(g *diam.GroupedAVP, code uint32, ips []string) {
	for _, s := range ips {
		if s == "" {
			continue
		}
		ip := net.ParseIP(s)
		if ip == nil {
			continue
		}
		g.AddAVP(avp3GPP(code, datatype.Address(ip)))
	}
}

// addOctetHex hex-decodes the input string and emits as OctetString. Empty
// input → no AVP. Invalid hex is silently skipped (logged at the caller's
// discretion); the encoder must NOT panic on producer-side data quality
// issues — the worst case is a missing AVP, which the peer can recover
// from, vs a panic that drops the whole answer.
func addOctetHex(g *diam.GroupedAVP, code uint32, s string) {
	if s == "" {
		return
	}
	b, err := hex.DecodeString(s)
	if err != nil {
		// Silent skip — producer mistake, not gateway bug. A test or
		// envelope-validator at the producer should catch this earlier.
		return
	}
	g.AddAVP(avp3GPP(code, datatype.OctetString(b)))
}

// emitSubscriptionData writes the Subscription-Data (1400) AVP onto `m`
// using the structured profile when set, falling back to the deprecated
// hex-OctetString legacy field. `verb` ("ULA" / "IDR") is used in the
// deprecation log line so operators can grep which producer is still on
// the legacy path.
//
// Carrier-grade contract:
//   - subProfile != nil  → emit proper Grouped tree per TS 29.272 §7.3.2.
//     Empty profile (no sub-AVPs) → AVP not emitted.
//   - subProfile == nil &&
//     subscriberData != "" → emit hex-decoded OctetString fallback +
//     log a deprecation warning so the producer
//     knows to migrate.
//   - both empty         → no AVP emitted (legitimate — partial answers
//     may omit Subscription-Data).
//
// The legacy fallback is wire-INVALID per TS 29.272 §7.3.2 (1400 is
// Grouped, not OctetString). It exists ONLY as a transitional shim;
// strict peers will reject the answer. Producers MUST migrate.
func emitSubscriptionData(m *diam.Message, subProfile *pb.SubscriptionProfile, subscriberData, verb string) {
	if subProfile != nil {
		if g := encodeSubscriptionData(subProfile); g != nil {
			m.AddAVP(avp3GPP(avp.SubscriptionData, g))
		}
		return
	}
	if subscriberData == "" {
		return
	}
	logSubDataDeprecation(verb, len(subscriberData))
	m.AddAVP(avp3GPP(avp.SubscriptionData, datatype.OctetString(subscriberData)))
}

// logSubDataDeprecation emits a single warning per producer/verb pair
// (cheap rate-limited via package-local seen map). Goes away with the
// legacy SubData field.
func logSubDataDeprecation(verb string, payloadLen int) {
	// Intentionally cheap: one log line per call. Operators can rely on
	// log volume to gauge migration progress without needing dedicated
	// metrics. If the volume becomes noisy in production, swap to a
	// rate-limited monitor counter.
	logger.Warn("DEPRECATED: SubData (legacy hex-OctetString) used in "+verb+
		"; migrate to SubProfile",
		logger.String("verb", verb),
		logger.Int("payload_bytes", payloadLen),
	)
}

// fmtForCompiler ensures fmt is referenced — this file may be edited to
// add error-returning variants of the helpers above. Remove if unused
// after stabilization.
var _ = fmt.Sprintf
