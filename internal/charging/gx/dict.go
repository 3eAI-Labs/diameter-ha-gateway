// Copyright 2026 3eAI Labs Ltd
// SPDX-License-Identifier: AGPL-3.0-only

package gx

import (
	"strings"

	"github.com/fiorix/go-diameter/v4/diam/dict"
)

// gxDictXML registers the Gx AVPs the translator reads from a CCR and
// go-diameter v4's Gx dictionary lacks. Undefined, they decoded as Unknown and
// the translator's type assertion panicked, which closed the PCEF's
// connection. Code, type and flags are those of TS 29.212 V17.3.0
// Table 5.3.0.1.
const gxDictXML = `<?xml version="1.0" encoding="UTF-8"?>
<diameter>
    <application id="16777238" type="auth" name="Gx Charging Control">
        <vendor id="10415" name="TGPP"/>
        <avp name="Bearer-Operation" code="1021" must="M,V" may="P" may-encrypt="Y" vendor-id="10415">
            <data type="Enumerated"/>
        </avp>
        <avp name="PDN-Connection-ID" code="1065" must="V" may="P" may-encrypt="Y" vendor-id="10415">
            <data type="OctetString"/>
        </avp>
        <avp name="AN-GW-Status" code="2811" must="V" may="P" must-not="M" may-encrypt="Y" vendor-id="10415">
            <data type="Enumerated"/>
        </avp>
    </application>
</diameter>`

func init() {
	if err := dict.Default.Load(strings.NewReader(gxDictXML)); err != nil {
		panic("gx: dictionary extension: " + err.Error())
	}
}
