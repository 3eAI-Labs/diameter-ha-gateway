// Copyright 2026 3eAI Labs Ltd
// SPDX-License-Identifier: AGPL-3.0-only

package diameter

import (
	"fmt"
	"strings"

	"github.com/fiorix/go-diameter/v4/diam/dict"
)

// AppIDS13 is the S13/S13' application (TS 29.272 §7.1.8), MME/SGSN ↔ EIR.
const AppIDS13 uint32 = 16777252

// s13DictXML registers the S13 application, which go-diameter's dictionaries
// do not carry. Without it the gateway could not parse an ME-Identity-Check
// request ("Could not find preloaded Command with code 324") and the
// connection was lost. Loading it into dict.Default before the state machine
// is built also makes the CEA advertise S13.
//
// References, TS 29.272 V17.8.0:
//   - §7.2.19, §7.2.20 ME-Identity-Check-Request/Answer ABNF (command 324)
//   - §7.3.3 Terminal-Information (1401), §7.3.4 IMEI (1402),
//     §7.3.5 Software-Version (1403), §7.3.6 3GPP2-MEID (1471)
//   - §7.3.51 Equipment-Status (1445)
//
// AVPs reused from the base protocol (Session-Id, DRMP, Origin-Host, …)
// resolve through the base dictionary.
const s13DictXML = `<?xml version="1.0" encoding="UTF-8"?>
<diameter>
    <application id="16777252" type="auth" name="TGPP S13">
        <vendor id="10415" name="TGPP"/>

        <command code="324" short="EC" name="ME-Identity-Check">
            <request>
                <rule avp="Session-Id" required="true" max="1"/>
                <rule avp="DRMP" required="false" max="1"/>
                <rule avp="Vendor-Specific-Application-Id" required="false" max="1"/>
                <rule avp="Auth-Session-State" required="true" max="1"/>
                <rule avp="Origin-Host" required="true" max="1"/>
                <rule avp="Origin-Realm" required="true" max="1"/>
                <rule avp="Destination-Host" required="false" max="1"/>
                <rule avp="Destination-Realm" required="true" max="1"/>
                <rule avp="Terminal-Information" required="true" max="1"/>
                <rule avp="User-Name" required="false" max="1"/>
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
                <rule avp="Equipment-Status" required="false" max="1"/>
                <rule avp="Failed-AVP" required="false" max="1"/>
                <rule avp="Proxy-Info" required="false"/>
                <rule avp="Route-Record" required="false"/>
            </answer>
        </command>

        <avp name="Terminal-Information" code="1401" must="M,V" may-encrypt="N" vendor-id="10415">
            <data type="Grouped"/>
        </avp>
        <avp name="IMEI" code="1402" must="M,V" may-encrypt="N" vendor-id="10415">
            <data type="UTF8String"/>
        </avp>
        <avp name="Software-Version" code="1403" must="M,V" may-encrypt="N" vendor-id="10415">
            <data type="UTF8String"/>
        </avp>
        <avp name="3GPP2-MEID" code="1471" must="M,V" may-encrypt="N" vendor-id="10415">
            <data type="OctetString"/>
        </avp>
        <avp name="Equipment-Status" code="1445" must="M,V" may-encrypt="N" vendor-id="10415">
            <data type="Enumerated">
                <item code="0" name="PERMITTEDLISTED"/>
                <item code="1" name="PROHIBITEDLISTED"/>
                <item code="2" name="TRACKINGLISTED"/>
            </data>
        </avp>
    </application>
</diameter>`

func init() {
	if err := dict.Default.Load(strings.NewReader(s13DictXML)); err != nil {
		panic(fmt.Sprintf("diameter: failed to register S13 in dict.Default: %v", err))
	}
}
