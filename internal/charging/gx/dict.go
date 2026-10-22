// Copyright 2026 3eAI Labs Ltd
// SPDX-License-Identifier: AGPL-3.0-only

package gx

import (
	"fmt"
	"sort"
	"strings"

	"github.com/fiorix/go-diameter/v4/diam/dict"
)

// gxDictXML registers every AVP of TS 29.212 Table 5.3.0.1 under the Gx
// application, with the code, type and M bit of spec29212. go-diameter v4's
// Gx dictionary lacks many of them, so they decoded as Unknown: the CCR
// handler panicked on some (Bearer-Operation, AN-GW-Status,
// PDN-Connection-ID) and Charging-Rule-Report was always empty; it gives
// others the wrong type (ToS-Traffic-Class as Unsigned32). A later Load of
// an AVP code replaces the earlier definition for the application.
//
// decoded29212 adds the AVPs a CCR may carry that the table does not
// define (TS 29.212 §5.6.2 ABNF), with the type and M bit of the
// specification the AVP comes from; without them a PCEF's CCR carrying one
// with the M bit was answered 5001 (RFC 6733 §4.1).
func gxDictXML() string {
	all := make(map[string]specAVP, len(spec29212)+len(decoded29212))
	for name, a := range spec29212 {
		all[name] = a
	}
	for name, a := range decoded29212 {
		all[name] = a
	}
	names := make([]string, 0, len(all))
	for name := range all {
		names = append(names, name)
	}
	sort.Strings(names)

	var b strings.Builder
	b.WriteString(`<?xml version="1.0" encoding="UTF-8"?>
<diameter>
    <application id="16777238" type="auth" name="Gx Charging Control">
        <vendor id="10415" name="TGPP"/>
`)
	for _, name := range names {
		a := all[name]
		must, mustNot := "V", "M"
		if a.MustM {
			must, mustNot = "M,V", ""
		}
		fmt.Fprintf(&b, "        <avp name=%q code=\"%d\" must=%q may=\"P\" must-not=%q may-encrypt=\"Y\" vendor-id=\"10415\"><data type=%q/></avp>\n",
			name, a.Code, must, mustNot, a.Type)
	}
	b.WriteString("    </application>\n</diameter>\n")
	return b.String()
}

// decoded29212 lists, for each AVP, the clause or table its type and M bit
// come from. TS 29.212 Table 5.3.0.2 (re-used AVPs) clears M on the 29.061,
// 29.272 and 29.273 ones; Tables 5a.3.1 (Gxx) and 5b.3.1 (Sd) and Annex
// E.6.3 set it.
var decoded29212 = map[string]specAVP{
	"3GPP-SGSN-IPv6-Address":            {15, "TS 29.061 §16.4.7; Table 5.3.0.2", "OctetString", false},
	"3GPP-GGSN-IPv6-Address":            {16, "TS 29.061 §16.4.7; Table 5.3.0.2", "OctetString", false},
	"TWAN-Identifier":                   {29, "TS 29.061 §16.4.7", "OctetString", false},
	"QoS-Rule-Report":                   {1055, "5a.3.5", "Grouped", true},
	"Session-Linking-Indicator":         {1064, "5a.3.6", "Enumerated", true},
	"ADC-Rule-Report":                   {1097, "5b.3.6", "Grouped", true},
	"Trace-Data":                        {1458, "TS 29.272 §7.3.63; Table 5.3.0.2", "Grouped", false},
	"Trace-Reference":                   {1459, "TS 29.272 §7.3.64; Table 5.3.0.2", "OctetString", false},
	"AN-Trusted":                        {1503, "TS 29.230 Table 7.1; Table 5.3.0.2", "Enumerated", false},
	"Origination-Time-Stamp":            {1536, "TS 29.230 Table 7.1 (TS 29.273; M not given)", "Unsigned64", false},
	"Maximum-Wait-Time":                 {1537, "TS 29.230 Table 7.1 (TS 29.273; M not given)", "Unsigned32", false},
	"CS-Service-QoS-Request-Identifier": {2807, "E.6.3.2", "OctetString", true},
	"CS-Service-QoS-Request-Operation":  {2808, "E.6.3.3", "Enumerated", true},
}

func init() {
	if err := dict.Default.Load(strings.NewReader(gxDictXML())); err != nil {
		panic("gx: dictionary extension: " + err.Error())
	}
}
