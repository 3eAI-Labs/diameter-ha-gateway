// Copyright 2026 3eAI Labs Ltd
// SPDX-License-Identifier: AGPL-3.0-only

package diameter

import (
	"strconv"

	"github.com/3eAI-Labs/diameter-ha-gateway/internal/platform/logger"
	"github.com/fiorix/go-diameter/v4/diam"
	"github.com/fiorix/go-diameter/v4/diam/avp"
	"github.com/fiorix/go-diameter/v4/diam/datatype"
)

const vendorID3GPP = 10415

// avp3GPP returns a vendor-10415 AVP with the V-bit set and the M-bit set
// only where TS 29.272 requires it (flags3GPP). Every 3GPP AVP the S6a and
// S13 encoders write goes through here: TS 29.272 Table 7.3.1/1 NOTE 2 says a
// receiver that does not understand an AVP with the M-bit set shall reject
// the message, so an M-bit on an AVP the specification sends without one
// turns an optional extension into a failed answer.
func avp3GPP(code uint32, data datatype.Type) *diam.AVP {
	return diam.NewAVP(code, flags3GPP(code), vendorID3GPP, data)
}

// flags3GPP is the header flags TS 29.272 sets for the vendor-10415 AVP code:
// from Table 7.3.1/1 for the AVPs it defines, from Table 7.3.1/2 for the ones
// it re-uses. A re-used AVP that Table 7.3.1/2 leaves blank follows its
// defining specification; reused29272 records what the gateway sends for it.
func flags3GPP(code uint32) uint8 {
	mustM := true
	if a, ok := spec29272ByCode[code]; ok {
		mustM = a.MustM
	} else if r, ok := reused29272[code]; ok {
		mustM = r.MustM
	}
	if mustM {
		return avp.Mbit | avp.Vbit
	}
	return avp.Vbit
}

var spec29272ByCode = func() map[uint32]specAVP {
	m := make(map[uint32]specAVP, len(spec29272))
	for _, a := range spec29272 {
		m[a.Code] = a
	}
	return m
}()

// reusedAVP is a vendor-10415 AVP that S6a/S13 carry but another
// specification defines.
type reusedAVP struct {
	Name  string
	Spec  string // the defining specification
	MustM bool
	// Table72 is true when TS 29.272 Table 7.3.1/2 sets the M-bit for S6a
	// ("Must set" / "Must not set"; its NOTE 1: this overrides the defining
	// specification). When false the defining specification applies, and
	// neither the code nor MustM has been checked against it: those
	// specifications are not in this repository.
	Table72 bool
}

// reused29272 lists the re-used vendor-10415 AVPs the gateway writes or
// reads. The TS 29.336 codes are checked against its Table 8.4.1.
var reused29272 = map[uint32]reusedAVP{
	13:   {"3GPP-Charging-Characteristics", "TS 29.061", true, false},
	515:  {"Max-Requested-Bandwidth-DL", "TS 29.214", true, false},
	516:  {"Max-Requested-Bandwidth-UL", "TS 29.214", true, false},
	554:  {"Extended-Max-Requested-BW-DL", "TS 29.214", false, false},
	555:  {"Extended-Max-Requested-BW-UL", "TS 29.214", false, false},
	600:  {"Visited-Network-Identifier", "TS 29.229", false, true},
	625:  {"Confidentiality-Key", "TS 29.229", true, false}, // Table 6.3.1: M, V
	626:  {"Integrity-Key", "TS 29.229", true, false},       // Table 6.3.1: M, V
	628:  {"Supported-Features", "TS 29.229", true, false},
	629:  {"Feature-List-ID", "TS 29.229", false, false}, // Table 6.3.1: V, must not M
	630:  {"Feature-List", "TS 29.229", false, false},    // Table 6.3.1: V, must not M
	701:  {"MSISDN", "TS 29.329", true, false},
	3329: {"Maximum-UE-Availability-Time", "TS 29.338", false, false}, // Table 5.3.3.1/1: V, must not M; read from NOR only
	848:  {"Served-Party-IP-Address", "TS 32.299", true, false},

	1028: {"QoS-Class-Identifier", "TS 29.212", true, false},
	1032: {"RAT-Type", "TS 29.212", true, true},
	1034: {"Allocation-Retention-Priority", "TS 29.212", true, false},
	1046: {"Priority-Level", "TS 29.212", true, false},
	1047: {"Pre-emption-Capability", "TS 29.212", true, false},
	1048: {"Pre-emption-Vulnerability", "TS 29.212", true, false},
	1227: {"PDP-Address", "TS 32.299", true, false},
	2405: {"GMLC-Address", "TS 29.173", false, true},

	3125: {"SCEF-ID", "TS 29.336", false, true},
	// Read from a ULR and a NOR (dict_extensions_29336.go); Table 7.3.1/2:
	// "Must not set". Their children are TS 29.336's, M per its Table 8.4.1.
	3124: {"SCEF-Reference-ID", "TS 29.336", false, true},
	3142: {"Monitoring-Event-Config-Status", "TS 29.336", false, true},
	3143: {"Supported-Services", "TS 29.336", false, true},
	3144: {"Supported-Monitoring-Events", "TS 29.336", false, true},
	3146: {"Service-Result", "TS 29.336", false, false},
	3147: {"Service-Result-Code", "TS 29.336", false, false},
	3152: {"Service-Report", "TS 29.336", false, false},
	3153: {"Node-Type", "TS 29.336", false, false},
	3186: {"SCEF-Reference-ID-Ext", "TS 29.336", false, false},
	1538: {"Emergency-Services", "TS 29.273", false, false}, // Table 7.3.1/2 gives no M-bit rule

	3701: {"ProSe-Subscription-Data", "TS 29.344", false, true},
	3702: {"ProSe-Permission", "TS 29.344", true, false},
}

// avpName is the TS 29.272 name of a 3GPP AVP code, or the code itself.
func avpName(code uint32) string {
	for name, a := range spec29272 {
		if a.Code == code {
			return name
		}
	}
	if r, ok := reused29272[code]; ok {
		return r.Name
	}
	switch code { // IETF AVPs S6a groups require
	case avp.ServiceSelection:
		return "Service-Selection"
	case avp.MIP6AgentInfo:
		return "MIP6-Agent-Info"
	}
	return strconv.FormatUint(uint64(code), 10)
}

// complete reports whether g carries every child its ABNF marks mandatory
// ({ ... }); codes are those children. A group missing one is not sent and
// the omission is logged: the producer's data is incomplete, and a peer may
// reject the whole message with DIAMETER_MISSING_AVP (5005, RFC 6733 §7.1.5)
// rather than ignore the group.
func complete(g *diam.GroupedAVP, parent string, codes ...uint32) bool {
	for _, c := range codes {
		found := false
		for _, a := range g.AVP {
			if a.Code == c {
				found = true
				break
			}
		}
		if !found {
			logger.Warn("S6a: "+parent+" not sent: mandatory AVP missing",
				logger.String("avp", avpName(c)))
			return false
		}
	}
	return true
}
