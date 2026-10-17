// Copyright 2026 3eAI Labs Ltd
// SPDX-License-Identifier: AGPL-3.0-only

package integration

import (
	"net"
	"testing"
	"time"

	"github.com/fiorix/go-diameter/v4/diam"
	"github.com/fiorix/go-diameter/v4/diam/avp"
	"github.com/fiorix/go-diameter/v4/diam/datatype"
	"github.com/fiorix/go-diameter/v4/diam/dict"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// exchange writes m on c and reads the answer.
func exchange(t *testing.T, c net.Conn, m *diam.Message) *diam.Message {
	t.Helper()
	_, err := m.WriteTo(c)
	require.NoError(t, err)
	require.NoError(t, c.SetReadDeadline(time.Now().Add(3*time.Second)))
	a, err := diam.ReadMessage(c, dict.Default)
	require.NoError(t, err)
	return a
}

func resultCodeOf(t *testing.T, m *diam.Message) uint32 {
	t.Helper()
	a, err := m.FindAVP(avp.ResultCode, 0)
	require.NoError(t, err)
	return uint32(a.Data.(datatype.Unsigned32))
}

// without returns m's AVPs except those coded code, re-encoded.
func without(t *testing.T, m *diam.Message, code uint32) *diam.Message {
	t.Helper()
	out := diam.NewMessage(m.Header.CommandCode, m.Header.CommandFlags, m.Header.ApplicationID,
		m.Header.HopByHopID, m.Header.EndToEndID, dict.Default)
	for _, a := range m.AVP {
		if a.Code != code {
			out.AddAVP(a)
		}
	}
	return out
}

// TestBaseProtocol_MalformedCER: a whitelisted peer's CER without a
// mandatory AVP is answered 5005, one whose Vendor-Specific-Application-Id
// holds both Auth- and Acct-Application-Id 5009 and one holding neither 5005
// (RFC 6733 §5.3.1, §6.11), each with a Failed-AVP, and the connection is
// closed. go-diameter's state machine answered all of them 2001.
func TestBaseProtocol_MalformedCER(t *testing.T) {
	peerAuthGateway(t, "127.0.0.1:3892")

	vsai := cer("mme.allowed.com")
	vsai.NewAVP(avp.VendorSpecificApplicationID, avp.Mbit, 0, &diam.GroupedAVP{AVP: []*diam.AVP{
		diam.NewAVP(avp.VendorID, avp.Mbit, 0, datatype.Unsigned32(10415)),
		diam.NewAVP(avp.AuthApplicationID, avp.Mbit, 0, datatype.Unsigned32(appS6a)),
		diam.NewAVP(avp.AcctApplicationID, avp.Mbit, 0, datatype.Unsigned32(appS6a)),
	}})

	vsaiNeither := cer("mme.allowed.com")
	vsaiNeither.NewAVP(avp.VendorSpecificApplicationID, avp.Mbit, 0, &diam.GroupedAVP{AVP: []*diam.AVP{
		diam.NewAVP(avp.VendorID, avp.Mbit, 0, datatype.Unsigned32(10415)),
	}})

	for name, tc := range map[string]struct {
		m      *diam.Message
		rc     uint32
		failed []uint32
	}{
		"no Product-Name": {without(t, cer("mme.allowed.com"), avp.ProductName), diam.MissingAVP, []uint32{avp.ProductName}},
		"no Vendor-Id":    {without(t, cer("mme.allowed.com"), avp.VendorID), diam.MissingAVP, []uint32{avp.VendorID}},
		"VSAI Auth+Acct":  {vsai, diam.AVPOccursTooManyTimes, []uint32{avp.AuthApplicationID, avp.AcctApplicationID}},
		"VSAI neither":    {vsaiNeither, diam.MissingAVP, []uint32{avp.AuthApplicationID, avp.AcctApplicationID}},
	} {
		c, err := net.Dial("tcp", "127.0.0.1:3892")
		require.NoError(t, err, name)
		cea := exchange(t, c, tc.m)
		assert.Equal(t, tc.rc, resultCodeOf(t, cea), name)
		f, err := cea.FindAVP(avp.FailedAVP, 0)
		require.NoError(t, err, name)
		var failed []uint32
		for _, child := range f.Data.(*diam.GroupedAVP).AVP {
			failed = append(failed, child.Code)
		}
		assert.Equal(t, tc.failed, failed, name)
		for _, code := range []uint32{avp.HostIPAddress, avp.VendorID, avp.ProductName} {
			_, err := cea.FindAVP(code, 0)
			assert.NoError(t, err, "%s: an error CEA carries AVP %d (RFC 6733 §5.3.2)", name, code)
		}

		_, err = diam.ReadMessage(c, dict.Default)
		require.Error(t, err, name)
		if ne, ok := err.(net.Error); ok && ne.Timeout() {
			t.Errorf("%s: the connection was left open", name)
		}
		c.Close()
	}
}

// TestBaseProtocol_AnswerFlagsAndProxyInfo, over a real connection:
//   - a protocol error answer (3002: no application serves PUR here) has the
//     E flag (RFC 6733 §7.1.3), not the T flag of the retransmitted request
//     (§3), and every Proxy-Info of the request, in order (§6.2);
//   - a request for a command the gateway does not serve is answered 3001,
//     one for an application it does not know 3007, with the E flag;
//     go-diameter left both unanswered.
func TestBaseProtocol_AnswerFlagsAndProxyInfo(t *testing.T) {
	peerAuthGateway(t, "127.0.0.1:3893")
	c, err := net.Dial("tcp", "127.0.0.1:3893")
	require.NoError(t, err)
	defer c.Close()
	require.Equal(t, uint32(diam.Success), resultCodeOf(t, exchange(t, c, cer("mme.allowed.com"))))

	pur := diam.NewMessage(diam.PurgeUE, diam.RequestFlag|diam.ProxiableFlag|diam.RetransmittedFlag, appS6a, 0, 0, dict.Default)
	pur.NewAVP(avp.SessionID, avp.Mbit, 0, datatype.UTF8String("mme.allowed.com;1;1"))
	pur.NewAVP(avp.AuthSessionState, avp.Mbit, 0, datatype.Enumerated(1))
	pur.NewAVP(avp.OriginHost, avp.Mbit, 0, datatype.DiameterIdentity("mme.allowed.com"))
	pur.NewAVP(avp.OriginRealm, avp.Mbit, 0, datatype.DiameterIdentity("test.com"))
	pur.NewAVP(avp.DestinationRealm, avp.Mbit, 0, datatype.DiameterIdentity("test.com"))
	pur.NewAVP(avp.UserName, avp.Mbit, 0, datatype.UTF8String("001010000000001"))
	for _, host := range []string{"dra1.test.com", "dra2.test.com"} {
		pur.NewAVP(avp.ProxyInfo, avp.Mbit, 0, &diam.GroupedAVP{AVP: []*diam.AVP{
			diam.NewAVP(avp.ProxyHost, avp.Mbit, 0, datatype.DiameterIdentity(host)),
			diam.NewAVP(avp.ProxyState, avp.Mbit, 0, datatype.OctetString(host)),
		}})
	}
	pua := exchange(t, c, pur)
	assert.Equal(t, uint32(diam.UnableToDeliver), resultCodeOf(t, pua))
	assert.NotZero(t, pua.Header.CommandFlags&diam.ErrorFlag, "E flag on 3002")
	assert.Zero(t, pua.Header.CommandFlags&diam.RetransmittedFlag, "no T flag on an answer")
	var hosts []string
	for _, a := range pua.AVP {
		if a.Code == avp.ProxyInfo {
			for _, child := range a.Data.(*diam.GroupedAVP).AVP {
				if child.Code == avp.ProxyHost {
					hosts = append(hosts, string(child.Data.(datatype.DiameterIdentity)))
				}
			}
		}
	}
	assert.Equal(t, []string{"dra1.test.com", "dra2.test.com"}, hosts)

	acr := diam.NewRequest(diam.Accounting, 3, dict.Default)
	acr.NewAVP(avp.SessionID, avp.Mbit, 0, datatype.UTF8String("mme.allowed.com;1;2"))
	acr.NewAVP(avp.OriginHost, avp.Mbit, 0, datatype.DiameterIdentity("mme.allowed.com"))
	acr.NewAVP(avp.OriginRealm, avp.Mbit, 0, datatype.DiameterIdentity("test.com"))
	aca := exchange(t, c, acr)
	assert.Equal(t, uint32(diam.CommandUnsupported), resultCodeOf(t, aca))
	assert.NotZero(t, aca.Header.CommandFlags&diam.ErrorFlag, "E flag on 3001")

	// A base command under an application the gateway knows nothing of:
	// 3007. (A command go-diameter cannot find for the Application-Id does
	// not decode, and go-diameter closes the connection before any handler.)
	unknownApp := diam.NewRequest(diam.SessionTermination, 99, dict.Default)
	unknownApp.NewAVP(avp.SessionID, avp.Mbit, 0, datatype.UTF8String("mme.allowed.com;1;3"))
	unknownApp.NewAVP(avp.OriginHost, avp.Mbit, 0, datatype.DiameterIdentity("mme.allowed.com"))
	unknownApp.NewAVP(avp.OriginRealm, avp.Mbit, 0, datatype.DiameterIdentity("test.com"))
	ans := exchange(t, c, unknownApp)
	assert.Equal(t, uint32(diam.ApplicationUnsupported), resultCodeOf(t, ans))
	assert.NotZero(t, ans.Header.CommandFlags&diam.ErrorFlag, "E flag on 3007")
}
