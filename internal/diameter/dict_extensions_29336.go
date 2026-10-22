// Copyright 2026 3eAI Labs Ltd
// SPDX-License-Identifier: AGPL-3.0-only

package diameter

import (
	"fmt"
	"strings"

	"github.com/fiorix/go-diameter/v4/diam/dict"
)

// dict29336XML registers the TS 29.336 AVPs an S6a message carries: SCEF-ID
// in APN-Configuration (TS 29.272 §7.3.35); Supported-Services in a ULR and
// Monitoring-Event-Config-Status in a NOR (§7.2.3, §7.2.17), with
// their children; and Emergency-Services of TS 29.273 (code and type from TS
// 29.230 Table 7.1), which a NOR carries. Codes and types are TS 29.336
// Table 8.4.1 (lines 2971-3052 of the ETSI text); TS 29.272 Table 7.3.1/2
// says each is sent with the M bit clear on S6a, and gives Emergency-Services
// no M-bit rule. A request carrying one of them with the M bit was answered
// 5001 once the gateway rejected unknown mandatory AVPs (RFC 6733 §4.1).
//
// AESE-Communication-Pattern (3113) and Monitoring-Event-Configuration (3122)
// are not supported: the gateway's encoding of both broke the ABNF TS 29.272
// gives them (§7.3.193, §7.3.195).
const dict29336XML = `<?xml version="1.0" encoding="UTF-8"?>
<diameter>
    <!-- Under S6a (16777251), not T6a: go-diameter's lookup falls back
         from S6a to Ro and the base dictionary only. -->
    <application id="16777251" type="auth" name="TGPP S6A">
        <vendor id="10415" name="TGPP"/>
        <avp name="SCEF-Reference-ID" code="3124" must="V" must-not="M" may-encrypt="N" vendor-id="10415"><data type="Unsigned32"/></avp>
        <avp name="SCEF-ID" code="3125" must="V" must-not="M" may-encrypt="N" vendor-id="10415"><data type="DiameterIdentity"/></avp>
        <avp name="Monitoring-Event-Config-Status" code="3142" must="V" must-not="M" may-encrypt="N" vendor-id="10415"><data type="Grouped"/></avp>
        <avp name="Supported-Services" code="3143" must="V" must-not="M" may-encrypt="N" vendor-id="10415"><data type="Grouped"/></avp>
        <avp name="Supported-Monitoring-Events" code="3144" must="V" must-not="M" may-encrypt="N" vendor-id="10415"><data type="Unsigned64"/></avp>
        <avp name="Service-Result" code="3146" must="V" may="M" may-encrypt="N" vendor-id="10415"><data type="Grouped"/></avp>
        <avp name="Service-Result-Code" code="3147" must="V" may="M" may-encrypt="N" vendor-id="10415"><data type="Unsigned32"/></avp>
        <avp name="Service-Report" code="3152" must="V" may="M" may-encrypt="N" vendor-id="10415"><data type="Grouped"/></avp>
        <avp name="Node-Type" code="3153" must="V" may="M" may-encrypt="N" vendor-id="10415"><data type="Unsigned32"/></avp>
        <avp name="SCEF-Reference-ID-Ext" code="3186" must="V" must-not="M" may-encrypt="N" vendor-id="10415"><data type="Unsigned64"/></avp>
        <avp name="Emergency-Services" code="1538" must="V" may="M" may-encrypt="N" vendor-id="10415"><data type="Unsigned32"/></avp>
    </application>
</diameter>`

func init() {
	if err := dict.Default.Load(strings.NewReader(dict29336XML)); err != nil {
		panic(fmt.Sprintf("diameter: failed to register TS 29.336 AVPs in dict.Default: %v", err))
	}
}
