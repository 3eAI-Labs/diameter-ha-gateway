// Copyright 2026 3eAI Labs Ltd
// SPDX-License-Identifier: AGPL-3.0-only

package diameter

import (
	"net"

	"github.com/fiorix/go-diameter/v4/diam"
	"github.com/fiorix/go-diameter/v4/diam/avp"
	"github.com/fiorix/go-diameter/v4/diam/datatype"
)

// checkCER finds what RFC 6733 requires of a CER and go-diameter's state
// machine does not check, which would otherwise be answered 2001:
//   - Host-IP-Address, Vendor-Id and Product-Name are mandatory (§5.3.1);
//     a CER without one is answered 5005 (DIAMETER_MISSING_AVP);
//   - a Vendor-Specific-Application-Id holds exactly one of
//     Auth-Application-Id and Acct-Application-Id (§6.11): one holding both
//     is answered 5009 (DIAMETER_AVP_OCCURS_TOO_MANY_TIMES) with the two
//     received AVPs, one holding neither 5005 with an example of each.
//
// It returns 0 for a CER that passes, otherwise the result code and the
// Failed-AVP children: a zero-valued example of each missing AVP (§7.5), or
// the received Auth- and Acct-Application-Id.
func checkCER(m *diam.Message) (uint32, []*diam.AVP) {
	var missing []*diam.AVP
	if !hasAVP(m, avp.HostIPAddress) {
		missing = append(missing, diam.NewAVP(avp.HostIPAddress, avp.Mbit, 0, datatype.Address(net.IPv4zero.To4())))
	}
	if !hasAVP(m, avp.VendorID) {
		missing = append(missing, diam.NewAVP(avp.VendorID, avp.Mbit, 0, datatype.Unsigned32(0)))
	}
	if !hasAVP(m, avp.ProductName) {
		missing = append(missing, diam.NewAVP(avp.ProductName, 0, 0, datatype.UTF8String("")))
	}
	if len(missing) > 0 {
		return diam.MissingAVP, missing
	}

	for _, a := range m.AVP {
		if a.Code != avp.VendorSpecificApplicationID || a.VendorID != 0 {
			continue
		}
		g, ok := a.Data.(*diam.GroupedAVP)
		if !ok {
			continue
		}
		var auth, acct *diam.AVP
		for _, child := range g.AVP {
			switch child.Code {
			case avp.AuthApplicationID:
				auth = child
			case avp.AcctApplicationID:
				acct = child
			}
		}
		switch {
		case auth != nil && acct != nil:
			return diam.AVPOccursTooManyTimes, []*diam.AVP{auth, acct}
		case auth == nil && acct == nil:
			return diam.MissingAVP, []*diam.AVP{
				diam.NewAVP(avp.AuthApplicationID, avp.Mbit, 0, datatype.Unsigned32(0)),
				diam.NewAVP(avp.AcctApplicationID, avp.Mbit, 0, datatype.Unsigned32(0)),
			}
		}
	}
	return 0, nil
}
