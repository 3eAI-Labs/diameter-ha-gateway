// Copyright 2026 3eAI Labs Ltd
// SPDX-License-Identifier: AGPL-3.0-only

package diameter

import (
	"fmt"
	"strings"

	"github.com/fiorix/go-diameter/v4/diam/dict"
)

// dict29272XML registers the TS 29.272 Subscription-Data AVPs the encoder
// writes and go-diameter v4's S6a dictionary lacks. Code, type and M-bit of
// every entry are those of Table 7.3.1/1 (spec29272);
// TestDictionary_MatchesTS29272 keeps them so.
//
// Not registered here because go-diameter already has them: Trace-Data
// children (1452, 1462-1466), MDT-Configuration (1622), Ext-PDP-Type (1620),
// Ext-PDP-Address (1621). PDP-Address (1227, TS 32.299) and RAT-Type (1032,
// TS 29.212) resolve through go-diameter's Ro dictionary, the S6a
// application's parent.
const dict29272XML = `<?xml version="1.0" encoding="UTF-8"?>
<diameter>
    <application id="16777251" type="auth" name="TGPP S6A">
        <vendor id="10415" name="TGPP"/>
        <!-- Area-Scope (§7.3.138) and its cell / area identities -->
        <avp name="Area-Scope" code="1624" must="V" must-not="M" may-encrypt="N" vendor-id="10415">
            <data type="Grouped"/>
        </avp>
        <avp name="Cell-Global-Identity" code="1604" must="V" must-not="M" may-encrypt="N" vendor-id="10415">
            <data type="OctetString"/>
        </avp>
        <avp name="E-UTRAN-Cell-Global-Identity" code="1602" must="V" must-not="M" may-encrypt="N" vendor-id="10415">
            <data type="OctetString"/>
        </avp>
        <avp name="Tracking-Area-Identity" code="1603" must="V" must-not="M" may-encrypt="N" vendor-id="10415">
            <data type="OctetString"/>
        </avp>
        <avp name="Routing-Area-Identity" code="1605" must="V" must-not="M" may-encrypt="N" vendor-id="10415">
            <data type="OctetString"/>
        </avp>
        <avp name="Location-Area-Identity" code="1606" must="V" must-not="M" may-encrypt="N" vendor-id="10415">
            <data type="OctetString"/>
        </avp>

        <!-- MDT-Configuration children (§7.3.136-§7.3.173) -->
        <avp name="Job-Type" code="1623" must="V" must-not="M" may-encrypt="N" vendor-id="10415">
            <data type="Enumerated"/>
        </avp>
        <avp name="List-Of-Measurements" code="1625" must="V" must-not="M" may-encrypt="N" vendor-id="10415">
            <data type="Unsigned32"/>
        </avp>
        <avp name="Reporting-Trigger" code="1626" must="V" must-not="M" may-encrypt="N" vendor-id="10415">
            <data type="Unsigned32"/>
        </avp>
        <avp name="Report-Interval" code="1627" must="V" must-not="M" may-encrypt="N" vendor-id="10415">
            <data type="Enumerated"/>
        </avp>
        <avp name="Report-Amount" code="1628" must="V" must-not="M" may-encrypt="N" vendor-id="10415">
            <data type="Enumerated"/>
        </avp>
        <avp name="Event-Threshold-RSRP" code="1629" must="V" must-not="M" may-encrypt="N" vendor-id="10415">
            <data type="Unsigned32"/>
        </avp>
        <avp name="Event-Threshold-RSRQ" code="1630" must="V" must-not="M" may-encrypt="N" vendor-id="10415">
            <data type="Unsigned32"/>
        </avp>
        <avp name="Logging-Interval" code="1631" must="V" must-not="M" may-encrypt="N" vendor-id="10415">
            <data type="Enumerated"/>
        </avp>
        <avp name="Logging-Duration" code="1632" must="V" must-not="M" may-encrypt="N" vendor-id="10415">
            <data type="Enumerated"/>
        </avp>
        <avp name="Measurement-Period-LTE" code="1655" must="V" must-not="M" may-encrypt="N" vendor-id="10415">
            <data type="Enumerated"/>
        </avp>
        <avp name="Measurement-Period-UMTS" code="1656" must="V" must-not="M" may-encrypt="N" vendor-id="10415">
            <data type="Enumerated"/>
        </avp>
        <avp name="Collection-Period-RRM-LTE" code="1657" must="V" must-not="M" may-encrypt="N" vendor-id="10415">
            <data type="Enumerated"/>
        </avp>
        <avp name="Collection-Period-RRM-UMTS" code="1658" must="V" must-not="M" may-encrypt="N" vendor-id="10415">
            <data type="Enumerated"/>
        </avp>
        <avp name="Positioning-Method" code="1659" must="V" must-not="M" may-encrypt="N" vendor-id="10415">
            <data type="OctetString"/>
        </avp>
        <avp name="Measurement-Quantity" code="1660" must="V" must-not="M" may-encrypt="N" vendor-id="10415">
            <data type="OctetString"/>
        </avp>
        <avp name="Event-Threshold-Event-1F" code="1661" must="V" must-not="M" may-encrypt="N" vendor-id="10415">
            <data type="Integer32"/>
        </avp>
        <avp name="Event-Threshold-Event-1I" code="1662" must="V" must-not="M" may-encrypt="N" vendor-id="10415">
            <data type="Integer32"/>
        </avp>

        <!-- IMSI-Group-Id (§7.3.189) -->
        <avp name="IMSI-Group-Id" code="1675" must="V" must-not="M" may-encrypt="N" vendor-id="10415">
            <data type="Grouped"/>
        </avp>
        <avp name="Group-Service-Id" code="1676" must="V" must-not="M" may-encrypt="N" vendor-id="10415">
            <data type="Unsigned32"/>
        </avp>
        <avp name="Group-PLMN-Id" code="1677" must="V" must-not="M" may-encrypt="N" vendor-id="10415">
            <data type="OctetString"/>
        </avp>
        <avp name="Local-Group-Id" code="1678" must="V" must-not="M" may-encrypt="N" vendor-id="10415">
            <data type="OctetString"/>
        </avp>

        <!-- Subscription-Data children -->
        <avp name="A-MSISDN" code="1643" must="V" must-not="M" may-encrypt="N" vendor-id="10415">
            <data type="OctetString"/>
        </avp>
        <avp name="Subscription-Data-Flags" code="1654" must="V" must-not="M" may-encrypt="N" vendor-id="10415">
            <data type="Unsigned32"/>
        </avp>
        <avp name="Adjacent-Access-Restriction-Data" code="1673" must="V" must-not="M" may-encrypt="N" vendor-id="10415">
            <data type="Grouped"/>
        </avp>
        <avp name="Emergency-Info" code="1687" must="V" must-not="M" may-encrypt="N" vendor-id="10415">
            <data type="Grouped"/>
        </avp>
        <avp name="V2X-Subscription-Data" code="1688" must="V" must-not="M" may-encrypt="N" vendor-id="10415">
            <data type="Grouped"/>
        </avp>
        <avp name="V2X-Permission" code="1689" must="V" must-not="M" may-encrypt="N" vendor-id="10415">
            <data type="Unsigned32"/>
        </avp>
        <avp name="eDRX-Cycle-Length" code="1691" must="V" must-not="M" may-encrypt="N" vendor-id="10415">
            <data type="Grouped"/>
        </avp>
        <avp name="eDRX-Cycle-Length-Value" code="1692" must="V" must-not="M" may-encrypt="N" vendor-id="10415">
            <data type="OctetString"/>
        </avp>
        <avp name="Paging-Time-Window" code="1701" must="V" must-not="M" may-encrypt="N" vendor-id="10415">
            <data type="Grouped"/>
        </avp>
        <avp name="Operation-Mode" code="1702" must="V" must-not="M" may-encrypt="N" vendor-id="10415">
            <data type="Unsigned32"/>
        </avp>
        <avp name="Paging-Time-Window-Length" code="1703" must="V" must-not="M" may-encrypt="N" vendor-id="10415">
            <data type="OctetString"/>
        </avp>
        <avp name="Service-Gap-Time" code="1698" must="V" must-not="M" may-encrypt="N" vendor-id="10415">
            <data type="Unsigned32"/>
        </avp>
        <avp name="DL-Buffering-Suggested-Packet-Count" code="1674" must="V" must-not="M" may-encrypt="N" vendor-id="10415">
            <data type="Integer32"/>
        </avp>
        <avp name="UE-Usage-Type" code="1680" must="V" must-not="M" may-encrypt="N" vendor-id="10415">
            <data type="Unsigned32"/>
        </avp>

        <!-- APN-Configuration and PDP-Context children -->
        <avp name="WLAN-offloadability" code="1667" must="V" must-not="M" may-encrypt="N" vendor-id="10415">
            <data type="Grouped"/>
        </avp>
        <avp name="WLAN-offloadability-EUTRAN" code="1668" must="V" must-not="M" may-encrypt="N" vendor-id="10415">
            <data type="Unsigned32"/>
        </avp>
        <avp name="WLAN-offloadability-UTRAN" code="1669" must="V" must-not="M" may-encrypt="N" vendor-id="10415">
            <data type="Unsigned32"/>
        </avp>
        <avp name="SIPTO-Local-Network-Permission" code="1665" must="V" must-not="M" may-encrypt="N" vendor-id="10415">
            <data type="Unsigned32"/>
        </avp>
        <avp name="Restoration-Priority" code="1663" must="V" must-not="M" may-encrypt="N" vendor-id="10415">
            <data type="Unsigned32"/>
        </avp>
        <avp name="Non-IP-PDN-Type-Indicator" code="1681" must="V" must-not="M" may-encrypt="N" vendor-id="10415">
            <data type="Enumerated"/>
        </avp>
        <avp name="Non-IP-Data-Delivery-Mechanism" code="1682" must="V" must-not="M" may-encrypt="N" vendor-id="10415">
            <data type="Unsigned32"/>
        </avp>
        <avp name="SCEF-Realm" code="1684" must="V" must-not="M" may-encrypt="N" vendor-id="10415">
            <data type="DiameterIdentity"/>
        </avp>
        <avp name="Preferred-Data-Mode" code="1686" must="V" must-not="M" may-encrypt="N" vendor-id="10415">
            <data type="Unsigned32"/>
        </avp>
        <avp name="PDN-Connection-Continuity" code="1690" must="V" must-not="M" may-encrypt="N" vendor-id="10415">
            <data type="Unsigned32"/>
        </avp>
    </application>
</diameter>`

func init() {
	if err := dict.Default.Load(strings.NewReader(dict29272XML)); err != nil {
		panic(fmt.Sprintf("diameter: failed to register TS 29.272 AVPs in dict.Default: %v", err))
	}
}
