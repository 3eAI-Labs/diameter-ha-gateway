// Copyright 2026 3eAI Labs Ltd
// SPDX-License-Identifier: AGPL-3.0-only

package diameter

import (
	"testing"

	"github.com/fiorix/go-diameter/v4/diam/dict"
)

// Smoke tests for dict.Default registrations across the three
// dict_extensions_*.go files.
//
// Strategy: ScanAVP(code) is appID-independent; we only need to assert the
// AVP is preloaded. Encoder/decoder paths exercise the full datatype contract
// in the round-trip tests.

// TestDictExtensions_Base covers the base set registered in dict_extensions.go:
// commands 319/320 + IDR-Flags + DSR-Flags + DSA-Flags + Equivalent-PLMN-List + Reset-ID.
func TestDictExtensions_Base(t *testing.T) {
	t.Parallel()

	// Commands (S6a app 16777251).
	for _, cmdCode := range []uint32{319, 320} {
		if _, err := dict.Default.FindCommand(16777251, cmdCode); err != nil {
			t.Errorf("base dict: command %d not registered: %v", cmdCode, err)
		}
	}

	// AVPs registered by dict_extensions.go.
	codes := []uint32{
		1490, // IDR-Flags
		1421, // DSR-Flags
		1422, // DSA-Flags
		1637, // Equivalent-PLMN-List
		1670, // Reset-ID
	}
	assertAVPsPreloaded(t, codes, "base set")
}

// TestDictExtensions_29272 covers TS 29.272 §7.3.x AVPs registered in
// dict_extensions_29272.go.
func TestDictExtensions_29272(t *testing.T) {
	t.Parallel()

	codes := []uint32{
		// Area-Scope sub-AVPs + wrapper
		1602, 1603, 1604, 1605, 1606, 1624,
		// MDT-Configuration sub-AVPs (Job-Type → Logging-Duration)
		1623, 1625, 1626, 1627, 1628, 1629, 1630, 1631, 1632,
		// MDT extended (Rel-12)
		1655, 1656, 1657, 1658, 1659, 1660, 1661, 1662,
		// IMSI-Group-Id family
		1675, 1676, 1677, 1678,
		// Adjacent-Access-Restriction-Data (Rel-14)
		1673,
		// V2X
		1688, 1689,
		// Emergency-Info (Rel-14)
		1687,
		// eDRX-Cycle-Length, Paging-Time-Window, Service-Gap-Time
		1691, 1692, 1701, 1702, 1703, 1698,
		// WLAN-Offloadability
		1667, 1668, 1669,
		// Top-level extras
		1663, 1674, 1680, 1681, 1682, 1684, 1686, 1690,
		// SIPTO-Local-Network-Permission
		1665,
	}
	assertAVPsPreloaded(t, codes, "TS 29.272 AVPs")
}

// TestDictExtensions_29336 covers TS 29.336 SCEF/MEC AVPs.
func TestDictExtensions_29336(t *testing.T) {
	t.Parallel()

	codes := []uint32{
		3125, // SCEF-ID
	}
	assertAVPsPreloaded(t, codes, "TS 29.336 SCEF/MEC AVPs")
}

// TestDictExtensions_FiorixCoverage_AlreadyRegistered guards the engineering
// judgment that 13 codes were *intentionally omitted* from the dict
// extensions because fiorix v4.0.4's dict.Default already ships them. If
// a future fiorix bump removes any of these, this test fires and forces the
// dict_extensions_29272.go list to be re-evaluated for re-registration.
func TestDictExtensions_FiorixCoverage_AlreadyRegistered(t *testing.T) {
	t.Parallel()

	codes := []uint32{
		// Trace family (TS 29.272 §7.3.95-104)
		1452, 1462, 1463, 1464, 1465, 1466, 1622,
		// Ext-PDP family (TS 29.272 §7.3.62)
		1620, 1621,
		// Base IETF
		848,  // Served-Party-IP-Address (TS 32.299 / RFC 4006 charging)
		554,  // Extended-Max-Requested-BW-UL (RFC 8506)
		555,  // Extended-Max-Requested-BW-DL (RFC 8506)
		1227, // PDP-Address (TS 32.299)
		1032, // RAT-Type (TS 29.212)
	}
	assertAVPsPreloaded(t, codes, "fiorix v4.0.4 already-shipped AVPs")
}

func assertAVPsPreloaded(t *testing.T, codes []uint32, scope string) {
	t.Helper()
	for _, code := range codes {
		avp, err := dict.Default.ScanAVP(code)
		if err != nil {
			t.Errorf("%s: AVP %d not preloaded in dict.Default: %v", scope, code, err)
			continue
		}
		if avp == nil {
			t.Errorf("%s: AVP %d ScanAVP returned nil avp without error", scope, code)
		}
	}
}
