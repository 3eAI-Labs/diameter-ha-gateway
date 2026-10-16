// Copyright 2026 3eAI Labs Ltd
// SPDX-License-Identifier: AGPL-3.0-only

package diameter

import (
	"fmt"
	"strings"

	"github.com/fiorix/go-diameter/v4/diam/dict"
)

// go-diameter v4's bundled S6a application (16777251) registers UL/CL/AI/PU
// (commands 316, 317, 318, 321) but omits Insert-Subscriber-Data (319),
// Delete-Subscriber-Data (320), the IDR-Flags AVP (1490), DSR-Flags AVP
// (1421), and DSA-Flags AVP (1422) — all vendor 10415. We register them on
// dict.Default at package init so prod and test paths share one enriched
// dictionary — avoiding test-only/prod divergence on Subscription-Data wire
// decode.
//
// dict.Parser.Load is additive on per-code lookup maps (FindCommand /
// FindAVP), so existing UL/CL/AI/PU entries survive this Load call.
//
// References:
//   - 3GPP TS 29.272 §6.1.4 (Insert/Delete-Subscriber-Data Request/Answer ABNF)
//   - 3GPP TS 29.272 §7.3.103 (IDR-Flags Unsigned32, M+V bits)
//   - 3GPP TS 29.272 §7.3.25 (DSR-Flags 1421, Unsigned32, M+V bits)
//   - 3GPP TS 29.272 §7.3.26 (DSA-Flags 1422, Unsigned32, M+V bits)
//   - 3GPP TS 29.272 §7.3.151 (Equivalent-PLMN-List 1637, Grouped, V)
//   - 3GPP TS 29.272 §7.3.184 (Reset-ID 1670, OctetString, V)
//   - RFC 5447 §4.2.1, RFC 4004 §7.4/§7.11 (MIP6-Agent-Info family, vendor 0)

const idrDictXML = `<?xml version="1.0" encoding="UTF-8"?>
<diameter>
    <application id="16777251" type="auth" name="TGPP S6A">
        <vendor id="10415" name="TGPP"/>

        <command code="319" short="ID" name="Insert-Subscriber-Data">
            <request>
                <rule avp="Session-Id" required="true" max="1"/>
                <rule avp="DRMP" required="false" max="1"/>
                <rule avp="Vendor-Specific-Application-Id" required="false" max="1"/>
                <rule avp="Auth-Session-State" required="true" max="1"/>
                <rule avp="Origin-Host" required="true" max="1"/>
                <rule avp="Origin-Realm" required="true" max="1"/>
                <rule avp="Destination-Host" required="true" max="1"/>
                <rule avp="Destination-Realm" required="true" max="1"/>
                <rule avp="User-Name" required="true" max="1"/>
                <rule avp="Supported-Features" required="false"/>
                <rule avp="IDR-Flags" required="false" max="1"/>
                <rule avp="Subscription-Data" required="true" max="1"/>
                <rule avp="AVP" required="false"/>
                <rule avp="Proxy-Info" required="false"/>
                <rule avp="Route-Record" required="false"/>
            </request>
            <answer>
                <rule avp="Session-Id" required="true" max="1"/>
                <rule avp="DRMP" required="false" max="1"/>
                <rule avp="Vendor-Specific-Application-Id" required="false" max="1"/>
                <rule avp="Supported-Features" required="false"/>
                <rule avp="Result-Code" required="false" max="1"/>
                <rule avp="Experimental-Result" required="false" max="1"/>
                <rule avp="Auth-Session-State" required="true" max="1"/>
                <rule avp="Origin-Host" required="true" max="1"/>
                <rule avp="Origin-Realm" required="true" max="1"/>
                <rule avp="AVP" required="false"/>
                <rule avp="Failed-AVP" required="false"/>
                <rule avp="Proxy-Info" required="false"/>
                <rule avp="Route-Record" required="false"/>
            </answer>
        </command>

        <avp name="IDR-Flags" code="1490" must="M,V" may-encrypt="N" vendor-id="10415">
            <data type="Unsigned32"/>
        </avp>

        <command code="320" short="DS" name="Delete-Subscriber-Data">
            <request>
                <rule avp="Session-Id" required="true" max="1"/>
                <rule avp="DRMP" required="false" max="1"/>
                <rule avp="Vendor-Specific-Application-Id" required="false" max="1"/>
                <rule avp="Auth-Session-State" required="true" max="1"/>
                <rule avp="Origin-Host" required="true" max="1"/>
                <rule avp="Origin-Realm" required="true" max="1"/>
                <rule avp="Destination-Host" required="true" max="1"/>
                <rule avp="Destination-Realm" required="true" max="1"/>
                <rule avp="User-Name" required="true" max="1"/>
                <rule avp="Supported-Features" required="false"/>
                <rule avp="DSR-Flags" required="true" max="1"/>
                <rule avp="Context-Identifier" required="false"/>
                <rule avp="Trace-Reference" required="false" max="1"/>
                <rule avp="TS-Code" required="false"/>
                <rule avp="SS-Code" required="false"/>
                <rule avp="AVP" required="false"/>
                <rule avp="Proxy-Info" required="false"/>
                <rule avp="Route-Record" required="false"/>
            </request>
            <answer>
                <rule avp="Session-Id" required="true" max="1"/>
                <rule avp="DRMP" required="false" max="1"/>
                <rule avp="Vendor-Specific-Application-Id" required="false" max="1"/>
                <rule avp="Result-Code" required="false" max="1"/>
                <rule avp="Experimental-Result" required="false" max="1"/>
                <rule avp="Auth-Session-State" required="true" max="1"/>
                <rule avp="Origin-Host" required="true" max="1"/>
                <rule avp="Origin-Realm" required="true" max="1"/>
                <rule avp="Supported-Features" required="false"/>
                <rule avp="DSA-Flags" required="false" max="1"/>
                <rule avp="AVP" required="false"/>
                <rule avp="Failed-AVP" required="false"/>
                <rule avp="Proxy-Info" required="false"/>
                <rule avp="Route-Record" required="false"/>
            </answer>
        </command>

        <avp name="DSR-Flags" code="1421" must="M,V" may-encrypt="N" vendor-id="10415">
            <data type="Unsigned32"/>
        </avp>

        <avp name="DSA-Flags" code="1422" must="M,V" may-encrypt="N" vendor-id="10415">
            <data type="Unsigned32"/>
        </avp>

        <!-- Equivalent-PLMN-List ::= <AVP header: 1637 10415>
                 1*{ Visited-PLMN-Id } *[AVP]   (ULR only) -->
        <avp name="Equivalent-PLMN-List" code="1637" must="V" must-not="M" may-encrypt="N" vendor-id="10415">
            <data type="Grouped"/>
        </avp>

        <avp name="Reset-ID" code="1670" must="V" must-not="M" may-encrypt="N" vendor-id="10415">
            <data type="OctetString"/>
        </avp>

        <!-- Service-Selection is an IETF AVP without vendor (RFC 5778 §6.2,
             reused by TS 29.272 §7.3.36). go-diameter's S6a dictionary gives
             it vendor 10415, so the AVP an MME sends decoded as Unknown. -->
        <avp name="Service-Selection" code="493" must="M" may-encrypt="N" vendor-id="0">
            <data type="UTF8String"/>
        </avp>

        <!-- ProSe-Subscription-Data (TS 29.344, re-used by TS 29.272
             Table 7.3.1/2, M-bit must not be set) and its ProSe-Permission.
             Codes and types not checked against TS 29.344 (reused29272). -->
        <avp name="ProSe-Subscription-Data" code="3701" must="V" must-not="M" may-encrypt="N" vendor-id="10415">
            <data type="Grouped"/>
        </avp>
        <avp name="ProSe-Permission" code="3702" must="M,V" may-encrypt="N" vendor-id="10415">
            <data type="Unsigned32"/>
        </avp>

        <!-- The MIP6-Agent-Info family is IETF too (RFC 5447, RFC 4004),
             re-used by TS 29.272 Table 7.3.1/2; go-diameter registers it
             under vendor 10415, which no peer sends. -->
        <avp name="MIP6-Agent-Info" code="486" must="M" must-not="V" may-encrypt="N" vendor-id="0">
            <data type="Grouped"/>
        </avp>
        <avp name="MIP-Home-Agent-Address" code="334" must="M" must-not="V" may-encrypt="N" vendor-id="0">
            <data type="Address"/>
        </avp>
        <avp name="MIP-Home-Agent-Host" code="348" must="M" must-not="V" may-encrypt="N" vendor-id="0">
            <data type="Grouped"/>
        </avp>
        <avp name="MIP6-Home-Link-Prefix" code="125" must="M" must-not="V" may-encrypt="N" vendor-id="0">
            <data type="OctetString"/>
        </avp>
    </application>
</diameter>`

func init() {
	if err := dict.Default.Load(strings.NewReader(idrDictXML)); err != nil {
		panic(fmt.Sprintf("diameter: failed to register IDR/IDR-Flags in dict.Default: %v", err))
	}
}
