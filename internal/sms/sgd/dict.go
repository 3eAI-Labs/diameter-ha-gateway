// Copyright 2026 3eAI Labs Ltd
// SPDX-License-Identifier: AGPL-3.0-only

package sgd

import (
	"fmt"
	"strings"

	"github.com/fiorix/go-diameter/v4/diam/dict"
)

// command is one S6c or SGd command as go-diameter's dictionary needs it.
// go-diameter routes a message to the handler registered as short+"R" or
// short+"A" (diam/server.go), and refuses to decode a command without AVP
// rules; it also decodes an AVP only by the definitions of the message's own
// Application-Id or of application 0. The rules list each command's Command
// Code Format (TS 29.338 §5.3.2, §6.3.2) in order.
type command struct {
	app      uint32
	code     uint32
	short    string
	name     string
	request  []string
	answer   []string
	required []string // the request's {fixed} AVPs beyond the common ones
}

// commonRequest and commonAnswer open every request and answer.
var (
	commonRequest = []string{"Session-Id", "DRMP", "Vendor-Specific-Application-Id",
		"Auth-Session-State", "Origin-Host", "Origin-Realm", "Destination-Host", "Destination-Realm"}
	commonAnswer = []string{"Session-Id", "DRMP", "Vendor-Specific-Application-Id",
		"Result-Code", "Experimental-Result", "Auth-Session-State", "Origin-Host", "Origin-Realm"}
	commonTail = []string{"Failed-AVP", "Proxy-Info", "Route-Record"}
)

var commands = []command{
	{
		app: AppIDS6c, code: CommandSRR, short: "SR", name: "Send-Routing-Info-for-SM",
		request: []string{"MSISDN", "User-Name", "SMSMI-Correlation-ID", "Supported-Features",
			"SC-Address", "SM-RP-MTI", "SM-RP-SMEA", "SRR-Flags", "SM-Delivery-Not-Intended"},
		answer: []string{"User-Name", "Supported-Features", "Serving-Node", "Additional-Serving-Node",
			"SMSF-3GPP-Address", "SMSF-Non-3GPP-Address", "LMSI", "User-Identifier", "MWD-Status",
			"MME-Absent-User-Diagnostic-SM", "MSC-Absent-User-Diagnostic-SM",
			"SGSN-Absent-User-Diagnostic-SM", "SMSF-3GPP-Absent-User-Diagnostic-SM",
			"SMSF-Non-3GPP-Absent-User-Diagnostic-SM"},
	},
	{
		app: AppIDS6c, code: CommandALR, short: "AL", name: "Alert-Service-Centre",
		request: []string{"SC-Address", "User-Identifier", "SMSMI-Correlation-ID",
			"Maximum-UE-Availability-Time", "SMS-GMSC-Alert-Event", "Serving-Node", "Supported-Features"},
		answer:   []string{"Supported-Features"},
		required: []string{"SC-Address", "User-Identifier"},
	},
	{
		app: AppIDS6c, code: CommandRDR, short: "RD", name: "Report-SM-Delivery-Status",
		request: []string{"Supported-Features", "User-Identifier", "SMSMI-Correlation-ID",
			"SC-Address", "SM-Delivery-Outcome", "RDR-Flags"},
		answer:   []string{"Supported-Features", "User-Identifier"},
		required: []string{"User-Identifier", "SC-Address", "SM-Delivery-Outcome"},
	},
	{
		app: AppIDSGd, code: CommandOFR, short: "OF", name: "MO-Forward-Short-Message",
		request: []string{"SC-Address", "OFR-Flags", "Supported-Features", "User-Identifier",
			"SM-RP-UI", "SMSMI-Correlation-ID", "SM-Delivery-Outcome"},
		answer: []string{"Supported-Features", "SM-Delivery-Failure-Cause", "SM-RP-UI",
			"External-Identifier"},
		required: []string{"SC-Address", "User-Identifier", "SM-RP-UI"},
	},
	{
		app: AppIDSGd, code: CommandTFR, short: "TF", name: "MT-Forward-Short-Message",
		request: []string{"User-Name", "Supported-Features", "SMSMI-Correlation-ID", "SC-Address",
			"SM-RP-UI", "MME-Number-for-MT-SMS", "SGSN-Number", "TFR-Flags", "SM-Delivery-Timer",
			"SM-Delivery-Start-Time", "Maximum-Retransmission-Time", "SMS-GMSC-Address"},
		answer: []string{"Supported-Features", "Absent-User-Diagnostic-SM",
			"SM-Delivery-Failure-Cause", "SM-RP-UI", "Requested-Retransmission-Time", "User-Identifier"},
		required: []string{"Destination-Host", "User-Name", "SC-Address", "SM-RP-UI"},
	},
}

// dictXML renders both applications.
func dictXML() string {
	var b strings.Builder
	b.WriteString(`<?xml version="1.0" encoding="UTF-8"?>` + "\n<diameter>\n")
	for _, app := range []struct {
		id   uint32
		name string
	}{{AppIDS6c, "S6c"}, {AppIDSGd, "SGd"}} {
		fmt.Fprintf(&b, "<application id=\"%d\" type=\"auth\" name=\"%s\">\n<vendor id=\"%d\" name=\"3GPP\"/>\n",
			app.id, app.name, VendorID3GPP)
		for _, c := range commands {
			if c.app != app.id {
				continue
			}
			fmt.Fprintf(&b, "<command code=\"%d\" short=\"%s\" name=\"%s\">\n<request>\n", c.code, c.short, c.name)
			writeRules(&b, append(append(append([]string{}, commonRequest...), c.request...), "Proxy-Info", "Route-Record"))
			b.WriteString("</request>\n<answer>\n")
			writeRules(&b, append(append(append([]string{}, commonAnswer...), c.answer...), commonTail...))
			b.WriteString("</answer>\n</command>\n")
		}
		for _, d := range avpDefs {
			if (app.id == AppIDS6c && !d.inS6c) || (app.id == AppIDSGd && !d.inSGd) {
				continue
			}
			must, mustNot := "V", "M"
			if (app.id == AppIDS6c && d.mS6c) || (app.id == AppIDSGd && d.mSGd) {
				must, mustNot = "M,V", ""
			}
			fmt.Fprintf(&b, "<avp name=\"%s\" code=\"%d\" must=\"%s\" must-not=\"%s\" may-encrypt=\"N\" vendor-id=\"%d\">\n<data type=\"%s\">\n",
				d.name, d.code, must, mustNot, VendorID3GPP, d.typ)
			writeRules(&b, groupRules[d.code])
			b.WriteString("</data>\n</avp>\n")
		}
		b.WriteString("</application>\n")
	}
	b.WriteString("</diameter>\n")
	return b.String()
}

func writeRules(b *strings.Builder, avps []string) {
	for _, name := range avps {
		fmt.Fprintf(b, "<rule avp=\"%s\" required=\"false\"/>\n", name)
	}
}

func init() {
	if err := dict.Default.Load(strings.NewReader(dictXML())); err != nil {
		panic("sgd: dictionary: " + err.Error())
	}
}
