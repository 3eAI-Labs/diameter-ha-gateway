// Copyright 2026 3eAI Labs Ltd
// SPDX-License-Identifier: AGPL-3.0-only

package sgd

import (
	"strings"

	"github.com/3eAI-Labs/diameter-ha-gateway/internal/platform/logger"
	"github.com/fiorix/go-diameter/v4/diam/dict"
)

// sgdDictXML contains the SGd dictionary embedded as a string
// This avoids embed path issues and ensures the dictionary is always available
// Note: Answer commands use the same code as requests but different short names for routing
const sgdDictXML = `<?xml version="1.0" encoding="UTF-8"?>
<diameter>
  <application id="16777312" name="SGd" type="auth">
    <vendor id="10415"/>
    <command code="8388645" name="MO-Forward-Short-Message" short="OFR" proxyable="true">
      <request/>
      <answer short="OFA"/>
    </command>
    <command code="8388646" name="MT-Forward-Short-Message" short="TFR" proxyable="true">
      <request/>
      <answer short="TFA"/>
    </command>
    <command code="8388647" name="Send-Routing-Info-for-SM" short="SRR" proxyable="true">
      <request/>
      <answer short="SRA"/>
    </command>
    <command code="8388648" name="Alert-Service-Centre" short="ALR" proxyable="true">
      <request/>
      <answer short="ALA"/>
    </command>
  </application>
</diameter>`

func init() {
	// Load SGd dictionary on package init
	if err := LoadDictionary(); err != nil {
		logger.Warn("Failed to load SGd dictionary, using default",
			logger.String("error", err.Error()))
	}
}

// LoadDictionary loads the SGd dictionary into go-diameter's default parser
func LoadDictionary() error {
	// Parse and add to default dictionary
	if err := dict.Default.Load(strings.NewReader(sgdDictXML)); err != nil {
		return err
	}

	logger.Info("SGd dictionary loaded successfully",
		logger.Int("app_id", int(AppIDSGd)))

	return nil
}

// CommandNames maps command codes to short names
var CommandNames = map[uint32]string{
	CommandOFR: "OFR",
	CommandTFR: "TFR",
	CommandSRR: "SRR",
	CommandALR: "ALR",
}

// CommandFullNames maps command codes to full names
var CommandFullNames = map[uint32]string{
	CommandOFR: "MO-Forward-Short-Message",
	CommandTFR: "MT-Forward-Short-Message",
	CommandSRR: "Send-Routing-Info-for-SM",
	CommandALR: "Alert-Service-Centre",
}
