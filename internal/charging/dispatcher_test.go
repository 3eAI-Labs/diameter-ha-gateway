// Copyright 2026 3eAI Labs Ltd
// SPDX-License-Identifier: AGPL-3.0-only

package charging

import (
	"testing"

	"github.com/fiorix/go-diameter/v4/diam"
	"github.com/fiorix/go-diameter/v4/diam/avp"
	"github.com/fiorix/go-diameter/v4/diam/datatype"
	"github.com/fiorix/go-diameter/v4/diam/dict"
	"github.com/stretchr/testify/assert"
)

type recorder struct {
	name string
	hit  *string
}

func (r recorder) HandleCCR(diam.Conn, *diam.Message) { *r.hit = r.name }

// TestDispatcher_RoutesByServiceContextID: a CCR reaches the interface its
// Service-Context-Id is configured for. The dispatcher compared go-diameter's
// debug string ("UTF8String{...},Padding:n") with the configured ids, so no
// CCR matched and, with no default configured, every CCR was rejected.
func TestDispatcher_RoutesByServiceContextID(t *testing.T) {
	var hit string
	d := NewDispatcher(recorder{"gx", &hit}, recorder{"gy", &hit}, recorder{"ro", &hit},
		[]string{"pcrf@3gpp.org"}, []string{"ocs.diameter@3gpp.org"}, []string{"32260@3gpp.org"},
		recorder{"default", &hit})

	for scid, want := range map[string]string{
		"pcrf@3gpp.org":         "gx",
		"ocs.diameter@3gpp.org": "gy",
		"32260@3gpp.org":        "ro",
		"other@example":         "default",
	} {
		hit = ""
		m := diam.NewRequest(272, 4, dict.Default)
		m.NewAVP(avp.ServiceContextID, avp.Mbit, 0, datatype.UTF8String(scid))
		d.HandleCCR(nil, m)
		assert.Equal(t, want, hit, scid)
	}
}
