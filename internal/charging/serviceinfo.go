// Copyright 2026 3eAI Labs Ltd
// SPDX-License-Identifier: AGPL-3.0-only

package charging

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net"
	"sort"
	"strings"
	"time"

	"github.com/fiorix/go-diameter/v4/diam"
	"github.com/fiorix/go-diameter/v4/diam/avp"
	"github.com/fiorix/go-diameter/v4/diam/datatype"
	"github.com/fiorix/go-diameter/v4/diam/dict"
)

const codeServiceInformation = 873

// AVP is one AVP of a Service-Information (TS 32.299 §7.2.183), as a CCR
// carries it or a CCA is to: every child at every depth, in order, so the
// application sees all of PS-Information, IMS-Information and the other
// service-specific groups, including what the gateway has no field for.
//
// Value is by type: a JSON string for UTF8String, DiameterIdentity,
// DiameterURI and IPFilterRule; base64 for OctetString; a number for the
// integer, Enumerated and float types; RFC 3339 for Time; the address for an
// IPv4 or IPv6 Address, base64 of family and address for another family
// (an E.164 number, family 8). A grouped AVP has AVPs
// and no Value. An AVP the dictionary does not know has no Name and its
// data in base64, with Type "Unknown".
type AVP struct {
	Name     string          `json:"name,omitempty"`
	Code     uint32          `json:"code"`
	VendorID uint32          `json:"vendor_id,omitempty"`
	Type     string          `json:"type,omitempty"`
	Value    json.RawMessage `json:"value,omitempty"`
	AVPs     []AVP           `json:"avps,omitempty"`
}

type serviceInfoAVP struct {
	Code   uint32
	Vendor uint32
	Type   string
	MustM  bool
	Source string
}

var serviceInfoByCode = func() map[[2]uint32]serviceInfoAVP {
	m := make(map[[2]uint32]serviceInfoAVP, len(specServiceInfo))
	for _, a := range specServiceInfo {
		m[[2]uint32{a.Code, a.Vendor}] = a
	}
	return m
}()

// serviceInfoDictXML registers the vendor AVPs of specServiceInfo under
// Credit-Control (Application-Id 4) with their specification's name, type
// and M bit. go-diameter v4 lacks 78 of them, so they decoded as Unknown,
// names others after RADIUS ("TGPP-Charging-Id") and gives
// Application-Service-Provider-Identity the wrong type. A later Load of an
// AVP code replaces the earlier definition for the application. The IETF
// AVPs (vendor 0) are the base dictionary's. Only the 3GPP vendor is
// declared: go-diameter advertises every application's vendor in the CEA,
// and the 3GPP2 and ETSI AVPs need none to be looked up.
func serviceInfoDictXML() string {
	names := make([]string, 0, len(specServiceInfo))
	for name, a := range specServiceInfo {
		if a.Vendor != 0 {
			names = append(names, name)
		}
	}
	sort.Strings(names)
	var b strings.Builder
	fmt.Fprintf(&b, `<?xml version="1.0" encoding="UTF-8"?>
<diameter>
    <application id="4" type="auth" name="Charging Control">
        <vendor id="%d" name="TGPP"/>
`, vendor3GPP)
	for _, name := range names {
		a := specServiceInfo[name]
		must, mustNot := "V", "M"
		if a.MustM {
			must, mustNot = "M,V", ""
		}
		fmt.Fprintf(&b, "        <avp name=%q code=\"%d\" must=%q may=\"P\" must-not=%q may-encrypt=\"Y\" vendor-id=\"%d\"><data type=%q/></avp>\n",
			name, a.Code, must, mustNot, a.Vendor, a.Type)
	}
	b.WriteString("    </application>\n</diameter>\n")
	return b.String()
}

func init() {
	if err := dict.Default.Load(strings.NewReader(serviceInfoDictXML())); err != nil {
		panic("charging: dictionary extension: " + err.Error())
	}
}

// ReadAVPs reads list whole.
func ReadAVPs(list []*diam.AVP) []AVP {
	if len(list) == 0 {
		return nil
	}
	out := make([]AVP, 0, len(list))
	for _, a := range list {
		out = append(out, readAVP(a))
	}
	return out
}

func readAVP(a *diam.AVP) AVP {
	out := AVP{Code: a.Code, VendorID: a.VendorID}
	if d, err := dict.Default.FindAVPWithVendor(AppIDCreditControl, a.Code, a.VendorID); err == nil {
		out.Name = d.Name
	}
	var v any
	switch d := a.Data.(type) {
	case *diam.GroupedAVP:
		out.Type = "Grouped"
		out.AVPs = ReadAVPs(d.AVP)
		if out.AVPs == nil {
			out.AVPs = []AVP{}
		}
		return out
	case datatype.UTF8String:
		v = string(d)
	case datatype.DiameterIdentity:
		v = string(d)
	case datatype.DiameterURI:
		v = string(d)
	case datatype.IPFilterRule:
		v = string(d)
	case datatype.OctetString:
		v = []byte(d)
	case datatype.Unsigned32:
		v = uint32(d)
	case datatype.Unsigned64:
		v = uint64(d)
	case datatype.Integer32:
		v = int32(d)
	case datatype.Integer64:
		v = int64(d)
	case datatype.Enumerated:
		v = int32(d)
	case datatype.Float32:
		v = float32(d)
	case datatype.Float64:
		v = float64(d)
	case datatype.Time:
		v = time.Time(d).UTC().Format(time.RFC3339)
	case datatype.Address:
		if isIP(d) {
			v = net.IP(d).String()
		} else {
			v = []byte(d)
		}
	default:
		out.Name = ""
		out.Type = "Unknown"
		v = a.Data.Serialize()
	}
	if out.Type == "" {
		out.Type = typeName(a.Data.Type())
	}
	out.Value, _ = json.Marshal(v)
	return out
}

// isIP tells an IPv4 or IPv6 Address, which go-diameter keeps without its
// family, from another family, which it keeps with it (datatype.Address).
// The two collide at 4 and 16 bytes; a family number (3-255) in the first
// two bytes would be an IPv4 address in 0.0.0.0/8 or an IPv6 one in ::/8,
// neither of which a node is given.
func isIP(b []byte) bool {
	if len(b) != net.IPv4len && len(b) != net.IPv6len {
		return false
	}
	family := uint16(b[0])<<8 | uint16(b[1])
	return family < 3 || family > 255
}

// rawAddress is an Address of another family than IPv4 and IPv6, family
// first, written as given: datatype.Address would write 4 or 16 bytes as an
// IP address.
type rawAddress []byte

func (a rawAddress) Serialize() []byte     { return []byte(a) }
func (a rawAddress) Len() int              { return len(a) }
func (a rawAddress) Padding() int          { return (4 - len(a)%4) % 4 }
func (a rawAddress) Type() datatype.TypeID { return datatype.AddressType }
func (a rawAddress) String() string        { return fmt.Sprintf("Address{%x}", []byte(a)) }

func typeName(t datatype.TypeID) string {
	for name, id := range datatype.Available {
		if id == t {
			return name
		}
	}
	return ""
}

// WriteAVPs builds list for a message. Each AVP needs its Code (or a Name
// the dictionary knows); a known AVP has the dictionary's type, and Type is
// refused when it differs; an unknown one is an OctetString unless Type
// says otherwise. The V bit follows VendorID; the M bit is the AVP's
// specification's (specServiceInfo, else the dictionary), and not set for
// an AVP neither knows.
func WriteAVPs(list []AVP) ([]*diam.AVP, error) {
	out := make([]*diam.AVP, 0, len(list))
	for i := range list {
		a, err := writeAVP(&list[i])
		if err != nil {
			return nil, err
		}
		out = append(out, a)
	}
	return out, nil
}

func writeAVP(in *AVP) (*diam.AVP, error) {
	code, vendor, typ := in.Code, in.VendorID, in.Type
	var d *dict.AVP
	if code == 0 && in.Name != "" {
		found, err := dict.Default.FindAVP(AppIDCreditControl, in.Name)
		if err != nil {
			return nil, fmt.Errorf("AVP %q: not in the dictionary", in.Name)
		}
		d, code, vendor = found, found.Code, found.VendorID
	} else if found, err := dict.Default.FindAVPWithVendor(AppIDCreditControl, code, vendor); err == nil {
		d = found
	}
	if code == 0 {
		return nil, fmt.Errorf("AVP without code or name")
	}
	if d != nil {
		if typ != "" && typ != "Unknown" && typ != d.Data.TypeName {
			return nil, fmt.Errorf("AVP %s is %s, not %s", d.Name, d.Data.TypeName, typ)
		}
		typ = d.Data.TypeName
	}
	if typ == "" || typ == "Unknown" {
		typ = "OctetString"
	}
	flags := uint8(0)
	if vendor != 0 {
		flags |= avp.Vbit
	}
	if s, ok := serviceInfoByCode[[2]uint32{code, vendor}]; ok {
		if s.MustM {
			flags |= avp.Mbit
		}
	} else if d != nil && strings.Contains(d.Must, "M") {
		flags |= avp.Mbit
	}
	label := in.Name
	if label == "" {
		label = fmt.Sprintf("%d/%d", code, vendor)
	}
	if typ == "Grouped" {
		kids, err := WriteAVPs(in.AVPs)
		if err != nil {
			return nil, err
		}
		return diam.NewAVP(code, flags, vendor, &diam.GroupedAVP{AVP: kids}), nil
	}
	v, err := value(typ, in.Value)
	if err != nil {
		return nil, fmt.Errorf("AVP %s: %v", label, err)
	}
	return diam.NewAVP(code, flags, vendor, v), nil
}

func value(typ string, raw json.RawMessage) (datatype.Type, error) {
	if len(raw) == 0 {
		return nil, fmt.Errorf("no value")
	}
	var s string
	str := func() error { return json.Unmarshal(raw, &s) }
	switch typ {
	case "UTF8String", "DiameterIdentity", "DiameterURI", "IPFilterRule":
		if err := str(); err != nil {
			return nil, err
		}
		switch typ {
		case "UTF8String":
			return datatype.UTF8String(s), nil
		case "DiameterIdentity":
			return datatype.DiameterIdentity(s), nil
		case "DiameterURI":
			return datatype.DiameterURI(s), nil
		}
		return datatype.IPFilterRule(s), nil
	case "OctetString":
		var b []byte
		if err := json.Unmarshal(raw, &b); err != nil {
			return nil, err
		}
		return datatype.OctetString(b), nil
	case "Unsigned32":
		var n uint32
		err := json.Unmarshal(raw, &n)
		return datatype.Unsigned32(n), err
	case "Unsigned64":
		var n uint64
		err := json.Unmarshal(raw, &n)
		return datatype.Unsigned64(n), err
	case "Integer32":
		var n int32
		err := json.Unmarshal(raw, &n)
		return datatype.Integer32(n), err
	case "Integer64":
		var n int64
		err := json.Unmarshal(raw, &n)
		return datatype.Integer64(n), err
	case "Enumerated":
		var n int32
		err := json.Unmarshal(raw, &n)
		return datatype.Enumerated(n), err
	case "Float32":
		var f float32
		err := json.Unmarshal(raw, &f)
		return datatype.Float32(f), err
	case "Float64":
		var f float64
		err := json.Unmarshal(raw, &f)
		return datatype.Float64(f), err
	case "Time":
		if err := str(); err != nil {
			return nil, err
		}
		t, err := time.Parse(time.RFC3339, s)
		return datatype.Time(t), err
	case "Address":
		if err := str(); err != nil {
			return nil, err
		}
		if ip := net.ParseIP(s); ip != nil {
			if ip4 := ip.To4(); ip4 != nil {
				return datatype.Address(ip4), nil
			}
			return datatype.Address(ip), nil
		}
		b, err := base64.StdEncoding.DecodeString(s)
		if err != nil {
			return nil, err
		}
		if len(b) < 2 {
			return nil, fmt.Errorf("address without a family")
		}
		return rawAddress(b), nil
	}
	return nil, fmt.Errorf("type %q is not supported", typ)
}

// ServiceInformationAVP is the Service-Information of a CCA, nil when list
// is empty (TS 32.299 §6.4.3).
func ServiceInformationAVP(list []AVP) (*diam.AVP, error) {
	if len(list) == 0 {
		return nil, nil
	}
	kids, err := WriteAVPs(list)
	if err != nil {
		return nil, fmt.Errorf("service_information.avps: %w", err)
	}
	return diam.NewAVP(codeServiceInformation, avp.Mbit|avp.Vbit, vendor3GPP, &diam.GroupedAVP{AVP: kids}), nil
}
