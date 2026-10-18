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
func gxDictXML() string {
	names := make([]string, 0, len(spec29212))
	for name := range spec29212 {
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
		a := spec29212[name]
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

func init() {
	if err := dict.Default.Load(strings.NewReader(gxDictXML())); err != nil {
		panic("gx: dictionary extension: " + err.Error())
	}
}
