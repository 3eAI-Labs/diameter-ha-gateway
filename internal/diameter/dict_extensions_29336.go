// Copyright 2026 3eAI Labs Ltd
// SPDX-License-Identifier: AGPL-3.0-only

package diameter

import (
	"fmt"
	"strings"

	"github.com/fiorix/go-diameter/v4/diam/dict"
)

// dict29336XML registers SCEF-ID, the one TS 29.336 AVP S6a carries in
// APN-Configuration (TS 29.272 §7.3.35). TS 29.272 Table 7.3.1/2 says it is
// sent with the M-bit clear on S6a. Its code is not checked: TS 29.336 is not
// in this repository.
//
// AESE-Communication-Pattern (3113) and Monitoring-Event-Configuration (3122)
// are not supported: the gateway's encoding of both broke the ABNF TS 29.272
// gives them (§7.3.193, §7.3.195), and their children's codes are TS 29.336's.
const dict29336XML = `<?xml version="1.0" encoding="UTF-8"?>
<diameter>
    <!-- Under S6a (16777251), not T6a: go-diameter's lookup falls back
         from S6a to Ro and the base dictionary only. -->
    <application id="16777251" type="auth" name="TGPP S6A">
        <vendor id="10415" name="TGPP"/>
        <avp name="SCEF-ID" code="3125" must="V" must-not="M" may-encrypt="N" vendor-id="10415">
            <data type="DiameterIdentity"/>
        </avp>
    </application>
</diameter>`

func init() {
	if err := dict.Default.Load(strings.NewReader(dict29336XML)); err != nil {
		panic(fmt.Sprintf("diameter: failed to register TS 29.336 AVPs in dict.Default: %v", err))
	}
}
