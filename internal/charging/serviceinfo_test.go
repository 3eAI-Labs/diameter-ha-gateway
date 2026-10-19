// Copyright 2026 3eAI Labs Ltd
// SPDX-License-Identifier: AGPL-3.0-only

package charging

import (
	"bytes"
	"encoding/json"
	"net"
	"sort"
	"testing"
	"time"

	"github.com/fiorix/go-diameter/v4/diam"
	"github.com/fiorix/go-diameter/v4/diam/avp"
	"github.com/fiorix/go-diameter/v4/diam/datatype"
	"github.com/fiorix/go-diameter/v4/diam/dict"
	"github.com/fiorix/go-diameter/v4/diam/sm"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestServiceInfoDictionary: every AVP a PS-Information or IMS-Information
// can carry is in the Credit-Control dictionary with its specification's
// type, and the vendor AVPs with its name. 78 of them were missing and
// decoded as Unknown; Application-Service-Provider-Identity was an
// OctetString (TS 29.214 §5.3.29: UTF8String).
func TestServiceInfoDictionary(t *testing.T) {
	assert.Len(t, specServiceInfo, 325)
	for name, a := range specServiceInfo {
		d, err := dict.Default.FindAVPWithVendor(AppIDCreditControl, a.Code, a.Vendor)
		if !assert.NoError(t, err, name) {
			continue
		}
		assert.Equal(t, a.Type, d.Data.TypeName, name)
		if a.Vendor != 0 {
			assert.Equal(t, name, d.Name)
		}
	}
}

func sample(t *testing.T, name string, a serviceInfoAVP) datatype.Type {
	switch a.Type {
	case "UTF8String":
		return datatype.UTF8String(name)
	case "DiameterIdentity":
		return datatype.DiameterIdentity("host.example")
	case "DiameterURI":
		return datatype.DiameterURI("aaa://host.example")
	case "IPFilterRule":
		return datatype.IPFilterRule("permit out ip from any to any")
	case "OctetString":
		return datatype.OctetString([]byte{0, 1, 0xfe})
	case "Unsigned32":
		return datatype.Unsigned32(0)
	case "Unsigned64":
		return datatype.Unsigned64(1 << 40)
	case "Integer32":
		return datatype.Integer32(-2)
	case "Integer64":
		return datatype.Integer64(-3)
	case "Enumerated":
		return datatype.Enumerated(1)
	case "Float32":
		return datatype.Float32(1.5)
	case "Time":
		return datatype.Time(time.Date(2026, 10, 19, 10, 0, 0, 0, time.UTC))
	case "Address":
		return datatype.Address(net.ParseIP("2001:db8::1"))
	case "Grouped":
		return &diam.GroupedAVP{}
	}
	t.Fatalf("%s: no sample for %s", name, a.Type)
	return nil
}

// TestServiceInfo_RoundTrip: a Service-Information holding every AVP of
// the table, and one the dictionary does not know, is read into AVPs with
// no Unknown but that one, passes through JSON, and is written back to the
// same bytes.
func TestServiceInfo_RoundTrip(t *testing.T) {
	names := make([]string, 0, len(specServiceInfo))
	for name := range specServiceInfo {
		names = append(names, name)
	}
	sort.Strings(names)
	var kids []*diam.AVP
	for _, name := range names {
		a := specServiceInfo[name]
		flags := uint8(0)
		if a.Vendor != 0 {
			flags |= avp.Vbit
		}
		if a.MustM {
			flags |= avp.Mbit
		}
		kids = append(kids, diam.NewAVP(a.Code, flags, a.Vendor, sample(t, name, a)))
	}
	kids = append(kids, diam.NewAVP(65000, avp.Vbit, 99999, datatype.OctetString("opaque")))
	in := diam.NewMessage(272, 0, 4, 1, 2, dict.Default)
	in.AddAVP(diam.NewAVP(codeServiceInformation, avp.Mbit|avp.Vbit, vendor3GPP, &diam.GroupedAVP{AVP: kids}))
	want, err := in.Serialize()
	require.NoError(t, err)

	got, err := diam.ReadMessage(bytes.NewReader(want), dict.Default)
	require.NoError(t, err)
	si, ok := got.AVP[0].Data.(*diam.GroupedAVP)
	require.True(t, ok)
	list := ReadAVPs(si.AVP)
	require.Len(t, list, len(kids))
	for _, a := range list[:len(list)-1] {
		assert.NotEqual(t, "Unknown", a.Type, "%d/%d", a.Code, a.VendorID)
		assert.NotEmpty(t, a.Name, "%d/%d", a.Code, a.VendorID)
	}
	assert.Equal(t, AVP{Code: 65000, VendorID: 99999, Type: "Unknown", Value: json.RawMessage(`"b3BhcXVl"`)}, list[len(list)-1])

	b, err := json.Marshal(list)
	require.NoError(t, err)
	var back []AVP
	require.NoError(t, json.Unmarshal(b, &back))
	a, err := ServiceInformationAVP(back)
	require.NoError(t, err)
	out := diam.NewMessage(272, 0, 4, 1, 2, dict.Default)
	out.AddAVP(a)
	again, err := out.Serialize()
	require.NoError(t, err)
	assert.Equal(t, want, again, "written back to the same bytes")
}

// TestServiceInfo_ReadsAsJSON: values read as an application expects them,
// a nested group included.
func TestServiceInfo_ReadsAsJSON(t *testing.T) {
	ps := diam.NewAVP(874, avp.Mbit|avp.Vbit, vendor3GPP, &diam.GroupedAVP{AVP: []*diam.AVP{
		diam.NewAVP(2, avp.Vbit, vendor3GPP, datatype.OctetString([]byte{0, 0, 0, 7})),
		diam.NewAVP(1227, avp.Mbit|avp.Vbit, vendor3GPP, datatype.Address(net.ParseIP("192.0.2.9").To4())),
		diam.NewAVP(30, avp.Mbit, 0, datatype.UTF8String("internet")),
		diam.NewAVP(1016, avp.Mbit|avp.Vbit, vendor3GPP, &diam.GroupedAVP{AVP: []*diam.AVP{
			diam.NewAVP(1028, avp.Mbit|avp.Vbit, vendor3GPP, datatype.Enumerated(9)),
		}}),
	}})
	m := onWire(t, ps)
	b, err := json.Marshal(ReadAVPs(m.AVP))
	require.NoError(t, err)
	assert.JSONEq(t, `[{"name":"PS-Information","code":874,"vendor_id":10415,"type":"Grouped","avps":[
		{"name":"3GPP-Charging-Id","code":2,"vendor_id":10415,"type":"OctetString","value":"AAAABw=="},
		{"name":"PDP-Address","code":1227,"vendor_id":10415,"type":"Address","value":"192.0.2.9"},
		{"name":"Called-Station-Id","code":30,"type":"UTF8String","value":"internet"},
		{"name":"QoS-Information","code":1016,"vendor_id":10415,"type":"Grouped","avps":[
			{"name":"QoS-Class-Identifier","code":1028,"vendor_id":10415,"type":"Enumerated","value":9}]}]}]`, string(b))
}

// TestServiceInfo_WriteByName: an application can name an AVP instead of
// coding it; the M and V bits follow the specification; a value of the
// wrong type, or an AVP neither coded nor known, is refused.
func TestServiceInfo_WriteByName(t *testing.T) {
	a, err := ServiceInformationAVP([]AVP{{Name: "IMS-Information", AVPs: []AVP{
		{Name: "Node-Functionality", Value: json.RawMessage(`6`)},
		{Name: "Session-Priority", Value: json.RawMessage(`2`)},
	}}})
	require.NoError(t, err)
	ims := kids(a)[0]
	assert.Equal(t, uint32(876), ims.Code)
	assert.Equal(t, uint8(avp.Mbit|avp.Vbit), kids(ims)[0].Flags, "Node-Functionality: V,M")
	assert.Equal(t, uint8(avp.Vbit), kids(ims)[1].Flags, "Session-Priority: V (TS 29.229)")

	_, err = ServiceInformationAVP([]AVP{{Name: "Node-Functionality", Value: json.RawMessage(`"six"`)}})
	assert.Error(t, err)
	_, err = ServiceInformationAVP([]AVP{{Name: "No-Such-AVP", Value: json.RawMessage(`1`)}})
	assert.Error(t, err)
	assert.Nil(t, must(ServiceInformationAVP(nil)))
}

func must(a *diam.AVP, err error) *diam.AVP {
	if err != nil {
		panic(err)
	}
	return a
}

// TestServiceInfo_CEAVendors: the dictionary declares no vendor but 3GPP,
// so the CEA, which go-diameter builds from every loaded application,
// advertises no 3GPP2 or ETSI application.
func TestServiceInfo_CEAVendors(t *testing.T) {
	for _, a := range sm.PrepareSupportedApps(dict.Default) {
		assert.Contains(t, []uint32{0, vendor3GPP}, a.Vendor, "application %d", a.ID)
	}
}

// TestServiceInfo_NonIPAddress: an Address of another family than IPv4 and
// IPv6 — here an E.164 number, family 8, 16 bytes like an IPv6 address —
// passes as base64 and is written back as it came.
func TestServiceInfo_NonIPAddress(t *testing.T) {
	e164 := append([]byte{0, 8}, []byte("90555123456789")...)
	in := diam.NewMessage(272, 0, 4, 1, 2, dict.Default)
	in.AddAVP(diam.NewAVP(2712, avp.Mbit|avp.Vbit, vendor3GPP, rawAddress(e164)))
	want, err := in.Serialize()
	require.NoError(t, err)
	got, err := diam.ReadMessage(bytes.NewReader(want), dict.Default)
	require.NoError(t, err)
	list := ReadAVPs(got.AVP)
	assert.Equal(t, `"AAg5MDU1NTEyMzQ1Njc4OQ=="`, string(list[0].Value))

	a, err := WriteAVPs(list)
	require.NoError(t, err)
	out := diam.NewMessage(272, 0, 4, 1, 2, dict.Default)
	out.AddAVP(a[0])
	again, err := out.Serialize()
	require.NoError(t, err)
	assert.Equal(t, want, again)

	ip := ReadAVPs([]*diam.AVP{diam.NewAVP(2712, avp.Vbit, vendor3GPP, datatype.Address(net.ParseIP("::1")))})
	assert.Equal(t, `"::1"`, string(ip[0].Value), "::/8 below family 3 is an address")
}

// TestServiceInfo_WriteRefusals: an AVP needs a code or a known name; a
// known AVP is written with its type, a different one is refused; the 3GPP
// VSAs of TS 29.061 take the M bit (TS 32.299 §7.2.0).
func TestServiceInfo_WriteRefusals(t *testing.T) {
	_, err := WriteAVPs([]AVP{{Type: "UTF8String", Value: json.RawMessage(`"x"`)}})
	assert.Error(t, err)
	_, err = WriteAVPs([]AVP{{Name: "Node-Functionality", Type: "UTF8String", Value: json.RawMessage(`"x"`)}})
	assert.Error(t, err)
	a, err := WriteAVPs([]AVP{{Code: 2, VendorID: vendor3GPP, Value: json.RawMessage(`"AAAABw=="`)}})
	require.NoError(t, err)
	assert.Equal(t, uint8(avp.Mbit|avp.Vbit), a[0].Flags)
	a, err = WriteAVPs([]AVP{{Code: 65000, VendorID: 99999, Type: "Unsigned32", Value: json.RawMessage(`7`)}})
	require.NoError(t, err)
	assert.Equal(t, datatype.Unsigned32(7), a[0].Data, "an unknown AVP takes the type given")
	assert.Equal(t, uint8(avp.Vbit), a[0].Flags)
}
